// Package gen turns a list of OpenAPI documents into a toolspec.Catalog.
// It only runs at build time (see cmd/gen and `go generate`).
package gen

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the content of services.yaml.
type Config struct {
	// SpecBaseURL is used to resolve service specs that are not local files.
	// Can be overridden with the SPEC_BASE_URL environment variable.
	SpecBaseURL string    `yaml:"specBaseUrl"`
	Services    []Service `yaml:"services"`

	dir string // directory of the config file, for relative spec paths
}

// Service is one microservice.
type Service struct {
	// Name is used as tool name prefix, e.g. "account" -> "account_getUser".
	Name string `yaml:"name"`
	// Prefix is appended to HCLOUD_API_SERVER at runtime: <HCLOUD_API_SERVER><prefix><operation path>.
	Prefix string `yaml:"prefix"`
	// Spec is a local file (relative to services.yaml), an absolute URL, or a
	// path that is resolved against specBaseUrl.
	Spec string `yaml:"spec"`
	// IncludeDeprecated also exposes deprecated operations.
	IncludeDeprecated bool `yaml:"includeDeprecated"`
	// Exclude lists operationIds or "METHOD /path" entries to skip.
	Exclude []string `yaml:"exclude"`
}

var serviceNameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,24}$`)

// LoadConfig reads and validates a services.yaml file.
func LoadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.dir = filepath.Dir(path)
	if v := os.Getenv("SPEC_BASE_URL"); v != "" {
		cfg.SpecBaseURL = v
	}
	if cfg.SpecBaseURL == "" {
		cfg.SpecBaseURL = firstEnv("HCLOUD_API_SERVER")
	}
	if len(cfg.Services) == 0 {
		return nil, fmt.Errorf("%s: no services configured", path)
	}
	seen := map[string]bool{}
	for i := range cfg.Services {
		s := &cfg.Services[i]
		if !serviceNameRe.MatchString(s.Name) {
			return nil, fmt.Errorf("service %d: name %q must match %s", i, s.Name, serviceNameRe)
		}
		if seen[s.Name] {
			return nil, fmt.Errorf("duplicate service name %q", s.Name)
		}
		seen[s.Name] = true
		if s.Spec == "" {
			return nil, fmt.Errorf("service %q: spec is required", s.Name)
		}
		s.Prefix = normalizePrefix(s.Prefix)
	}
	return &cfg, nil
}

func normalizePrefix(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimRight(p, "/")
	if p != "" && !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

func firstEnv(names ...string) string {
	for _, n := range names {
		if v := strings.TrimSpace(os.Getenv(n)); v != "" {
			return v
		}
	}
	return ""
}
