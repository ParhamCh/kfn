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
		"missing name":     "image: x:1\n",
		"missing image":    "name: hello\n",
		"bad name":         "name: Hello_World\nimage: x:1\n",
		"bad port":         "name: hello\nimage: x:1\nport: 70000\n",
		"unknown field":    "name: hello\nimage: x:1\nreplica: 3\n", // typo of replicas
		"bad ingress host": "name: hello\nimage: x:1\ningress:\n  enabled: true\n  host: Bad_Host\n",
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
	// Monitoring disabled to keep this focused on the core two objects.
	spec := loadString(t, "name: hello\nimage: reg/hello:v1\nport: 9000\nmonitoring:\n  enabled: false\nenv:\n  - name: LOG_LEVEL\n    value: debug\n")
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

func TestIngressOffByDefault(t *testing.T) {
	for _, d := range renderDocs(t, loadString(t, minimalYAML)) {
		if d["kind"] == "Ingress" {
			t.Fatal("rendered an Ingress without ingress.enabled")
		}
	}
}

func TestIngressDefaults(t *testing.T) {
	spec := loadString(t, "name: hello\nimage: reg/hello:v1\ningress:\n  enabled: true\n")
	in := spec.Ingress
	if in.Host != "hello.kfn.lan" {
		t.Errorf("Host = %q, want hello.kfn.lan", in.Host)
	}
	if !in.UseTLS {
		t.Error("UseTLS = false, want true by default when enabled")
	}
	if in.ClusterIssuer != "cm-lab-ca" {
		t.Errorf("ClusterIssuer = %q, want cm-lab-ca", in.ClusterIssuer)
	}
	if in.ClassName != "nginx" {
		t.Errorf("ClassName = %q, want nginx", in.ClassName)
	}
	if in.SecretName != "hello-tls" {
		t.Errorf("SecretName = %q, want hello-tls", in.SecretName)
	}
	want := map[string]string{
		"nginx.ingress.kubernetes.io/proxy-body-size":    "1m",
		"nginx.ingress.kubernetes.io/proxy-read-timeout": "60",
		"nginx.ingress.kubernetes.io/proxy-send-timeout": "60",
		"cert-manager.io/cluster-issuer":                 "cm-lab-ca",
		"nginx.ingress.kubernetes.io/ssl-redirect":       "true",
	}
	for k, v := range want {
		if in.ResolvedAnnotations[k] != v {
			t.Errorf("annotation %q = %q, want %q", k, in.ResolvedAnnotations[k], v)
		}
	}
}

func TestIngressProxyTimeoutFollowsInvokeTimeout(t *testing.T) {
	spec := loadString(t, "name: hello\nimage: reg/hello:v1\nenv:\n  - name: INVOKE_TIMEOUT\n    value: 90s\ningress:\n  enabled: true\n")
	if got := spec.Ingress.ResolvedAnnotations["nginx.ingress.kubernetes.io/proxy-read-timeout"]; got != "120" {
		t.Errorf("proxy-read-timeout = %q, want 120 (90s + 30s headroom)", got)
	}
}

func TestIngressUserAnnotationWins(t *testing.T) {
	spec := loadString(t, "name: hello\nimage: reg/hello:v1\ningress:\n  enabled: true\n  annotations:\n    nginx.ingress.kubernetes.io/proxy-body-size: 10m\n")
	if got := spec.Ingress.ResolvedAnnotations["nginx.ingress.kubernetes.io/proxy-body-size"]; got != "10m" {
		t.Errorf("proxy-body-size = %q, want user override 10m", got)
	}
}

func TestIngressTLSDisabledDropsTLS(t *testing.T) {
	spec := loadString(t, "name: hello\nimage: reg/hello:v1\nmonitoring:\n  enabled: false\ningress:\n  enabled: true\n  tls: false\n")
	if spec.Ingress.UseTLS {
		t.Fatal("UseTLS = true, want false when tls: false")
	}
	ann := spec.Ingress.ResolvedAnnotations
	if _, ok := ann["cert-manager.io/cluster-issuer"]; ok {
		t.Error("cert-manager annotation present despite tls: false")
	}
	if _, ok := ann["nginx.ingress.kubernetes.io/ssl-redirect"]; ok {
		t.Error("ssl-redirect annotation present despite tls: false")
	}

	docs := renderDocs(t, spec)
	if len(docs) != 3 || docs[2]["kind"] != "Ingress" {
		t.Fatalf("want 3 docs ending in Ingress, got %d", len(docs))
	}
	if _, ok := mp(t, docs[2]["spec"])["tls"]; ok {
		t.Error("rendered Ingress has a tls block despite tls: false")
	}
}

func TestMonitoringOnByDefault(t *testing.T) {
	spec := loadString(t, minimalYAML)
	m := spec.Monitoring
	if !m.On {
		t.Fatal("Monitoring.On = false, want true by default")
	}
	if m.Port != 9090 {
		t.Errorf("Monitoring.Port = %d, want 9090", m.Port)
	}
	if m.Path != "/metrics" {
		t.Errorf("Monitoring.Path = %q, want /metrics", m.Path)
	}
	if m.ReleaseLabel != "kps" {
		t.Errorf("Monitoring.ReleaseLabel = %q, want kps", m.ReleaseLabel)
	}
}

func TestRenderServiceMonitor(t *testing.T) {
	// minimalYAML has ingress off, monitoring on → Deployment + Service + ServiceMonitor.
	docs := renderDocs(t, loadString(t, minimalYAML))
	if len(docs) != 3 {
		t.Fatalf("got %d docs, want 3 (Deployment + Service + ServiceMonitor)", len(docs))
	}
	sm := docs[2]
	if sm["kind"] != "ServiceMonitor" {
		t.Fatalf("third doc kind = %v, want ServiceMonitor", sm["kind"])
	}
	// Must carry the operator's discovery label, or it is never scraped.
	if mp(t, mp(t, sm["metadata"])["labels"])["release"] != "kps" {
		t.Errorf("ServiceMonitor missing release=kps label")
	}
	ep := mp(t, sl(t, mp(t, sm["spec"])["endpoints"])[0])
	if ep["port"] != "metrics" || ep["path"] != "/metrics" {
		t.Errorf("endpoint port/path = %v / %v, want metrics / /metrics", ep["port"], ep["path"])
	}
	nsel := sl(t, mp(t, mp(t, sm["spec"])["namespaceSelector"])["matchNames"])
	if nsel[0] != "kfn" {
		t.Errorf("namespaceSelector = %v, want [kfn]", nsel)
	}

	// Deployment + Service expose the metrics port.
	depPorts := sl(t, mp(t, sl(t, mp(t, mp(t, mp(t, docs[0]["spec"])["template"])["spec"])["containers"])[0])["ports"])
	if len(depPorts) != 2 || mp(t, depPorts[1])["name"] != "metrics" {
		t.Errorf("deployment ports = %v, want http + metrics", depPorts)
	}
	svcPorts := sl(t, mp(t, docs[1]["spec"])["ports"])
	if len(svcPorts) != 2 || mp(t, svcPorts[1])["name"] != "metrics" {
		t.Errorf("service ports = %v, want http + metrics", svcPorts)
	}
}

func TestMonitoringDisabledDropsEverything(t *testing.T) {
	spec := loadString(t, "name: hello\nimage: reg/hello:v1\nmonitoring:\n  enabled: false\n")
	if spec.Monitoring.On {
		t.Fatal("Monitoring.On = true, want false when enabled: false")
	}
	docs := renderDocs(t, spec)
	if len(docs) != 2 {
		t.Fatalf("got %d docs, want 2 (no ServiceMonitor when disabled)", len(docs))
	}
	// No metrics port on the Service either.
	if ports := sl(t, mp(t, docs[1]["spec"])["ports"]); len(ports) != 1 {
		t.Errorf("service has %d ports, want 1 (no metrics port when disabled)", len(ports))
	}
}

func TestRenderIngressDocument(t *testing.T) {
	spec := loadString(t, "name: hello\nimage: reg/hello:v1\nport: 8080\nmonitoring:\n  enabled: false\ningress:\n  enabled: true\n")
	docs := renderDocs(t, spec)
	if len(docs) != 3 {
		t.Fatalf("got %d docs, want 3 (Deployment + Service + Ingress)", len(docs))
	}
	ing := docs[2]
	if ing["kind"] != "Ingress" {
		t.Fatalf("third doc kind = %v, want Ingress", ing["kind"])
	}
	spc := mp(t, ing["spec"])
	if spc["ingressClassName"] != "nginx" {
		t.Errorf("ingressClassName = %v, want nginx", spc["ingressClassName"])
	}
	// TLS host + secret.
	tls := mp(t, sl(t, spc["tls"])[0])
	if h := sl(t, tls["hosts"]); h[0] != "hello.kfn.lan" {
		t.Errorf("tls host = %v, want hello.kfn.lan", h[0])
	}
	if tls["secretName"] != "hello-tls" {
		t.Errorf("tls secretName = %v, want hello-tls", tls["secretName"])
	}
	// Rule host + backend points at the Service port.
	rule := mp(t, sl(t, spc["rules"])[0])
	if rule["host"] != "hello.kfn.lan" {
		t.Errorf("rule host = %v, want hello.kfn.lan", rule["host"])
	}
	p := mp(t, sl(t, mp(t, rule["http"])["paths"])[0])
	backend := mp(t, mp(t, mp(t, p["backend"])["service"])["port"])
	if backend["number"] != 8080 {
		t.Errorf("backend service port = %v, want 8080", backend["number"])
	}
}
