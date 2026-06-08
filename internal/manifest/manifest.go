// Package manifest turns a function's function.yaml into the Kubernetes objects that
// deploy it: one Deployment and one Service per function. Rendering is done with
// text/template over a typed spec (no client-go), so the output is plain, auditable
// YAML. A ServiceMonitor is intentionally not emitted yet — it arrives in M5 alongside
// the /metrics endpoint it would scrape.
package manifest

import (
	"embed"
	"fmt"
	"io"
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

// FunctionSpec is the function.yaml schema plus derived/defaulted fields. It is the
// single source of data passed to the templates.
type FunctionSpec struct {
	Name         string            `yaml:"name"`
	Image        string            `yaml:"image"`
	Port         int               `yaml:"port"`
	Replicas     int               `yaml:"replicas"`
	Namespace    string            `yaml:"namespace"`
	NodeSelector map[string]string `yaml:"nodeSelector"`
	Resources    Resources         `yaml:"resources"`
	Env          []EnvVar          `yaml:"env"`
	ShutdownGrace string           `yaml:"shutdownGrace"`

	// TerminationGracePeriodSeconds is derived from ShutdownGrace; not read from YAML.
	TerminationGracePeriodSeconds int `yaml:"-"`
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
	return nil
}
