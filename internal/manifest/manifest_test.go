package manifest

import (
	"bytes"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const minimalYAML = `
name: hello
image: registry.example.com/hello:v1
`

func loadString(t *testing.T, s string) *FunctionSpec {
	t.Helper()
	spec, err := Load(strings.NewReader(s))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return spec
}

func TestLoadAppliesDefaults(t *testing.T) {
	spec := loadString(t, minimalYAML)

	if spec.Port != 8080 {
		t.Errorf("Port = %d, want 8080", spec.Port)
	}
	if spec.Replicas != 1 {
		t.Errorf("Replicas = %d, want 1", spec.Replicas)
	}
	if spec.Namespace != "kfn" {
		t.Errorf("Namespace = %q, want kfn", spec.Namespace)
	}
	if spec.NodeSelector["role"] != "workload" {
		t.Errorf("NodeSelector = %v, want role=workload", spec.NodeSelector)
	}
	if spec.Resources.Requests.CPU != "50m" || spec.Resources.Requests.Memory != "64Mi" {
		t.Errorf("request defaults = %+v", spec.Resources.Requests)
	}
	if spec.ShutdownGrace != "15s" {
		t.Errorf("ShutdownGrace = %q, want 15s", spec.ShutdownGrace)
	}
	if spec.TerminationGracePeriodSeconds != 20 { // 15s + 5s margin
		t.Errorf("TerminationGracePeriodSeconds = %d, want 20", spec.TerminationGracePeriodSeconds)
	}
}

func TestLoadValidation(t *testing.T) {
	cases := map[string]string{
		"missing name":  "image: x:1\n",
		"missing image": "name: hello\n",
		"bad name":      "name: Hello_World\nimage: x:1\n",
		"bad port":      "name: hello\nimage: x:1\nport: 70000\n",
		"unknown field": "name: hello\nimage: x:1\nreplica: 3\n", // typo of replicas
	}
	for name, yml := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(strings.NewReader(yml)); err == nil {
				t.Fatalf("expected error for %q, got nil", name)
			}
		})
	}
}

// renderDocs renders the spec and returns each YAML document as a generic map.
func renderDocs(t *testing.T, spec *FunctionSpec) []map[string]any {
	t.Helper()
	var buf bytes.Buffer
	if err := spec.Render(&buf); err != nil {
		t.Fatalf("Render: %v", err)
	}
	var docs []map[string]any
	for part := range strings.SplitSeq(buf.String(), "---") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		var m map[string]any
		if err := yaml.Unmarshal([]byte(part), &m); err != nil {
			t.Fatalf("rendered an invalid YAML document: %v\n%s", err, part)
		}
		docs = append(docs, m)
	}
	return docs
}

func mp(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %#v", v)
	}
	return m
}

func sl(t *testing.T, v any) []any {
	t.Helper()
	s, ok := v.([]any)
	if !ok {
		t.Fatalf("expected slice, got %#v", v)
	}
	return s
}

func TestRenderProducesDeploymentAndService(t *testing.T) {
	spec := loadString(t, "name: hello\nimage: reg/hello:v1\nport: 9000\nenv:\n  - name: LOG_LEVEL\n    value: debug\n")
	docs := renderDocs(t, spec)

	if len(docs) != 2 {
		t.Fatalf("got %d docs, want 2 (Deployment + Service)", len(docs))
	}
	if docs[0]["kind"] != "Deployment" || docs[1]["kind"] != "Service" {
		t.Fatalf("kinds = %v / %v", docs[0]["kind"], docs[1]["kind"])
	}

	// --- Deployment assertions ---
	dep := docs[0]
	if mp(t, dep["metadata"])["namespace"] != "kfn" {
		t.Errorf("deployment namespace = %v, want kfn", mp(t, dep["metadata"])["namespace"])
	}
	podSpec := mp(t, mp(t, mp(t, dep["spec"])["template"])["spec"])
	if mp(t, podSpec["nodeSelector"])["role"] != "workload" {
		t.Errorf("nodeSelector = %v, want role=workload", podSpec["nodeSelector"])
	}
	container := mp(t, sl(t, podSpec["containers"])[0])

	// FUNCTION_NAME must be injected from the function name.
	var gotFnName string
	for _, e := range sl(t, container["env"]) {
		em := mp(t, e)
		if em["name"] == "FUNCTION_NAME" {
			gotFnName, _ = em["value"].(string)
		}
	}
	if gotFnName != "hello" {
		t.Errorf("FUNCTION_NAME env = %q, want hello", gotFnName)
	}

	// Probes point at the runtime's endpoints.
	live := mp(t, mp(t, container["livenessProbe"])["httpGet"])
	ready := mp(t, mp(t, container["readinessProbe"])["httpGet"])
	if live["path"] != "/healthz" || ready["path"] != "/readyz" {
		t.Errorf("probe paths = %v / %v", live["path"], ready["path"])
	}

	// Port flows through to the container.
	port := mp(t, sl(t, container["ports"])[0])
	if port["containerPort"] != 9000 {
		t.Errorf("containerPort = %v, want 9000", port["containerPort"])
	}

	// --- Service assertions ---
	svc := docs[1]
	sport := mp(t, sl(t, mp(t, svc["spec"])["ports"])[0])
	if sport["port"] != 9000 || sport["targetPort"] != "http" {
		t.Errorf("service port/targetPort = %v / %v", sport["port"], sport["targetPort"])
	}
}
