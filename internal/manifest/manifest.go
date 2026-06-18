// Package manifest turns a function's function.yaml into the Kubernetes objects that
// deploy it: one Deployment and one Service per function, plus an optional Ingress (with
// cert-manager TLS) when the function opts into being exposed and a ServiceMonitor (on by
// default) that wires the /metrics endpoint into the prometheus-operator. Rendering is
// done with text/template over a typed spec (no client-go), so the output is plain,
// auditable YAML.
package manifest

import (
	"embed"
	"fmt"
	"io"
	"maps"
	"math"
	"regexp"
	"strconv"
	"text/template"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed templates/*.tmpl
var templates embed.FS

// dns1123Label matches a valid Kubernetes object name (RFC 1123 label).
var dns1123Label = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// dns1123Subdomain matches a valid DNS subdomain (e.g. an Ingress host like
// hello.kfn.lan): one or more dot-separated RFC 1123 labels.
var dns1123Subdomain = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)

// FunctionSpec is the function.yaml schema plus derived/defaulted fields. It is the
// single source of data passed to the templates.
type FunctionSpec struct {
	Name          string            `yaml:"name"`
	Image         string            `yaml:"image"`
	Port          int               `yaml:"port"`
	Replicas      int               `yaml:"replicas"`
	Namespace     string            `yaml:"namespace"`
	NodeSelector  map[string]string `yaml:"nodeSelector"`
	Resources     Resources         `yaml:"resources"`
	Env           []EnvVar          `yaml:"env"`
	ShutdownGrace string            `yaml:"shutdownGrace"`
	Ingress       Ingress           `yaml:"ingress"`
	Monitoring    Monitoring        `yaml:"monitoring"`

	// TerminationGracePeriodSeconds is derived from ShutdownGrace; not read from YAML.
	TerminationGracePeriodSeconds int `yaml:"-"`
}

// Monitoring controls the per-function Prometheus metrics surface: a dedicated metrics
// port on the Deployment/Service and a ServiceMonitor that the prometheus-operator
// scrapes. On by default. The ReleaseLabel is what gets the ServiceMonitor discovered by
// the operator (kube-prometheus-stack selects on release=<helm release>).
type Monitoring struct {
	Enabled      *bool  `yaml:"enabled"`      // default true
	Port         int    `yaml:"port"`         // default 9090 (matches the runtime's METRICS_PORT)
	Path         string `yaml:"path"`         // default /metrics
	Interval     string `yaml:"interval"`     // default 30s
	ReleaseLabel string `yaml:"releaseLabel"` // default kps; label the operator selects on

	On bool `yaml:"-"` // resolved: Enabled == nil || *Enabled
}

// Ingress optionally exposes the function through ingress-nginx at Host, with TLS
// issued by cert-manager. It is off unless Enabled. UseTLS and ResolvedAnnotations are
// derived during applyDefaults, not read from YAML.
type Ingress struct {
	Enabled       bool              `yaml:"enabled"`
	Host          string            `yaml:"host"`          // default <name>.kfn.lan
	TLS           *bool             `yaml:"tls"`           // default true when enabled
	ClusterIssuer string            `yaml:"clusterIssuer"` // default cm-lab-ca
	ClassName     string            `yaml:"className"`     // default nginx
	SecretName    string            `yaml:"secretName"`    // default <name>-tls
	Annotations   map[string]string `yaml:"annotations"`   // overrides merged over derived

	UseTLS              bool              `yaml:"-"`
	ResolvedAnnotations map[string]string `yaml:"-"`
}

// Resources holds container resource requests and limits.
type Resources struct {
	Requests ResourceList `yaml:"requests"`
	Limits   ResourceList `yaml:"limits"`
}

// ResourceList is a cpu/memory pair as Kubernetes quantity strings.
type ResourceList struct {
	CPU    string `yaml:"cpu"`
	Memory string `yaml:"memory"`
}

// EnvVar is a literal environment variable injected into the container.
type EnvVar struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

// Load decodes a function.yaml, applies defaults, and validates the result.
func Load(r io.Reader) (*FunctionSpec, error) {
	var spec FunctionSpec
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true) // reject unknown keys so typos surface early
	if err := dec.Decode(&spec); err != nil {
		return nil, fmt.Errorf("manifest: parse function.yaml: %w", err)
	}
	spec.applyDefaults()
	if err := spec.validate(); err != nil {
		return nil, err
	}
	return &spec, nil
}

func (s *FunctionSpec) applyDefaults() {
	if s.Port == 0 {
		s.Port = 8080
	}
	if s.Replicas == 0 {
		s.Replicas = 1
	}
	if s.Namespace == "" {
		s.Namespace = "kfn"
	}
	if s.NodeSelector == nil {
		// Pin functions to the workload nodes by default.
		s.NodeSelector = map[string]string{"role": "workload"}
	}
	if s.Resources.Requests.CPU == "" {
		s.Resources.Requests.CPU = "50m"
	}
	if s.Resources.Requests.Memory == "" {
		s.Resources.Requests.Memory = "64Mi"
	}
	if s.Resources.Limits.CPU == "" {
		s.Resources.Limits.CPU = "250m"
	}
	if s.Resources.Limits.Memory == "" {
		s.Resources.Limits.Memory = "128Mi"
	}
	if s.ShutdownGrace == "" {
		s.ShutdownGrace = "15s"
	}
	// Give the kubelet a margin beyond the runtime's drain window so a draining pod is
	// never SIGKILLed mid-flight. Falls back to a sane value on an unparseable grace.
	grace := 15 * time.Second
	if d, err := time.ParseDuration(s.ShutdownGrace); err == nil {
		grace = d
	}
	s.TerminationGracePeriodSeconds = int(math.Ceil(grace.Seconds())) + 5

	s.applyIngressDefaults()
	s.applyMonitoringDefaults()
}

// applyMonitoringDefaults fills in the Monitoring block. Metrics are on unless explicitly
// disabled. The defaults mirror the runtime (METRICS_PORT 9090) and the cluster's
// prometheus-operator (ServiceMonitors are discovered by the release=kps label).
func (s *FunctionSpec) applyMonitoringDefaults() {
	m := &s.Monitoring
	m.On = m.Enabled == nil || *m.Enabled
	// The runtime always serves /metrics on this port; monitoring.enabled only controls
	// whether it is exposed (a container/Service port) and scraped (a ServiceMonitor). So the
	// port is always resolved and validated, even when scraping is off.
	if m.Port == 0 {
		m.Port = 9090
	}
	if !m.On {
		return
	}
	if m.Path == "" {
		m.Path = "/metrics"
	}
	if m.Interval == "" {
		m.Interval = "30s"
	}
	if m.ReleaseLabel == "" {
		m.ReleaseLabel = "kps"
	}
}

// applyIngressDefaults fills in the Ingress block and resolves its nginx/cert-manager
// annotations from the runtime contract. A no-op unless ingress is enabled.
func (s *FunctionSpec) applyIngressDefaults() {
	in := &s.Ingress
	if !in.Enabled {
		return
	}
	if in.Host == "" {
		in.Host = s.Name + ".kfn.lan"
	}
	if in.ClassName == "" {
		in.ClassName = "nginx"
	}
	if in.ClusterIssuer == "" {
		in.ClusterIssuer = "cm-lab-ca"
	}
	if in.SecretName == "" {
		in.SecretName = s.Name + "-tls"
	}
	in.UseTLS = in.TLS == nil || *in.TLS

	// Derive annotations from the runtime's own limits so the edge agrees with the pod:
	// cap the body at the runtime's 1 MiB, and keep nginx's proxy timeout above
	// INVOKE_TIMEOUT so the runtime emits its own 504 before nginx severs the connection.
	proxyTimeout := invokeTimeoutSeconds(s.Env) + 30
	derived := map[string]string{
		"nginx.ingress.kubernetes.io/proxy-body-size":    "1m",
		"nginx.ingress.kubernetes.io/proxy-read-timeout": strconv.Itoa(proxyTimeout),
		"nginx.ingress.kubernetes.io/proxy-send-timeout": strconv.Itoa(proxyTimeout),
	}
	if in.UseTLS {
		derived["cert-manager.io/cluster-issuer"] = in.ClusterIssuer
		derived["nginx.ingress.kubernetes.io/ssl-redirect"] = "true"
	}
	// User-supplied annotations win over the derived defaults.
	maps.Copy(derived, in.Annotations)
	in.ResolvedAnnotations = derived
}

// invokeTimeoutSeconds returns the function's INVOKE_TIMEOUT (from env) in whole
// seconds, defaulting to 30 (the runtime default) when unset or unparseable.
func invokeTimeoutSeconds(env []EnvVar) int {
	for _, e := range env {
		if e.Name != "INVOKE_TIMEOUT" {
			continue
		}
		if d, err := time.ParseDuration(e.Value); err == nil && d > 0 {
			return int(math.Ceil(d.Seconds()))
		}
	}
	return 30
}

func (s *FunctionSpec) validate() error {
	if s.Name == "" {
		return fmt.Errorf("manifest: name is required")
	}
	if !dns1123Label.MatchString(s.Name) {
		return fmt.Errorf("manifest: name %q is not a valid DNS-1123 label", s.Name)
	}
	if s.Image == "" {
		return fmt.Errorf("manifest: image is required")
	}
	if s.Port < 1 || s.Port > 65535 {
		return fmt.Errorf("manifest: port %d out of range 1-65535", s.Port)
	}
	if s.Replicas < 0 {
		return fmt.Errorf("manifest: replicas %d must be >= 0", s.Replicas)
	}
	if s.Ingress.Enabled {
		if !dns1123Subdomain.MatchString(s.Ingress.Host) {
			return fmt.Errorf("manifest: ingress.host %q is not a valid DNS subdomain", s.Ingress.Host)
		}
		if s.Ingress.ClassName == "" {
			return fmt.Errorf("manifest: ingress.className is required")
		}
		if s.Ingress.UseTLS && s.Ingress.ClusterIssuer == "" {
			return fmt.Errorf("manifest: ingress.clusterIssuer is required when tls is enabled")
		}
	}
	// The runtime always binds the metrics port, so it must be valid and differ from the
	// function port whether or not scraping is enabled.
	if s.Monitoring.Port < 1 || s.Monitoring.Port > 65535 {
		return fmt.Errorf("manifest: monitoring.port %d out of range 1-65535", s.Monitoring.Port)
	}
	if s.Monitoring.Port == s.Port {
		return fmt.Errorf("manifest: monitoring.port %d must differ from the function port", s.Monitoring.Port)
	}
	return nil
}

var tmpl = template.Must(template.New("manifest").
	Funcs(template.FuncMap{"quote": strconv.Quote}).
	ParseFS(templates, "templates/*.tmpl"))

// Render writes the Deployment and Service YAML (separated by ---) for the function.
func (s *FunctionSpec) Render(w io.Writer) error {
	if _, err := io.WriteString(w, "---\n"); err != nil {
		return err
	}
	if err := tmpl.ExecuteTemplate(w, "deployment.yaml.tmpl", s); err != nil {
		return fmt.Errorf("manifest: render deployment: %w", err)
	}
	if _, err := io.WriteString(w, "---\n"); err != nil {
		return err
	}
	if err := tmpl.ExecuteTemplate(w, "service.yaml.tmpl", s); err != nil {
		return fmt.Errorf("manifest: render service: %w", err)
	}
	if s.Ingress.Enabled {
		if _, err := io.WriteString(w, "---\n"); err != nil {
			return err
		}
		if err := tmpl.ExecuteTemplate(w, "ingress.yaml.tmpl", s); err != nil {
			return fmt.Errorf("manifest: render ingress: %w", err)
		}
	}
	if s.Monitoring.On {
		if _, err := io.WriteString(w, "---\n"); err != nil {
			return err
		}
		if err := tmpl.ExecuteTemplate(w, "servicemonitor.yaml.tmpl", s); err != nil {
			return fmt.Errorf("manifest: render servicemonitor: %w", err)
		}
	}
	return nil
}
