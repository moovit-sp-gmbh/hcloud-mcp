package gen

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi2conv"
	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"
)

// authTransport adds SPEC_TOKEN (if set) to every spec download.
type authTransport struct {
	token string
	base  http.RoundTripper
}

func (t authTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if t.token != "" {
		r = r.Clone(r.Context())
		r.Header.Set("Authorization", "Bearer "+t.token)
	}
	return t.base.RoundTrip(r)
}

func specHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   60 * time.Second,
		Transport: authTransport{token: firstEnv("SPEC_TOKEN", "HCLOUD_API_TOKEN"), base: http.DefaultTransport},
	}
}

// resolveSpecLocation returns a URL for the spec of s: a file:// URL if the
// spec exists locally, otherwise an http(s) URL.
func (c *Config) resolveSpecLocation(s Service) (*url.URL, error) {
	if u, err := url.Parse(s.Spec); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		return u, nil
	}
	local := s.Spec
	if !filepath.IsAbs(local) {
		local = filepath.Join(c.dir, local)
	}
	if _, err := os.Stat(local); err == nil {
		abs, err := filepath.Abs(local)
		if err != nil {
			return nil, err
		}
		return &url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}, nil
	}
	if c.SpecBaseURL == "" {
		return nil, fmt.Errorf("spec %q is not a local file and no spec base URL is set (set HCLOUD_API_SERVER, SPEC_BASE_URL or specBaseUrl)", s.Spec)
	}
	return url.Parse(strings.TrimRight(c.SpecBaseURL, "/") + "/" + strings.TrimLeft(s.Spec, "/"))
}

func readLocation(ctx context.Context, cl *http.Client, u *url.URL) ([]byte, error) {
	if u.Scheme == "file" {
		return os.ReadFile(filepath.FromSlash(u.Path))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json, application/yaml;q=0.9, */*;q=0.8")
	resp, err := cl.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

// loadSpec loads an OpenAPI 3.x or Swagger 2.0 document, resolves all refs and
// internalizes external refs so every schema ref points into
// #/components/schemas.
func (c *Config) loadSpec(ctx context.Context, s Service) (*openapi3.T, *url.URL, error) {
	loc, err := c.resolveSpecLocation(s)
	if err != nil {
		return nil, nil, err
	}
	cl := specHTTPClient()
	data, err := readLocation(ctx, cl, loc)
	if err != nil {
		return nil, loc, err
	}

	var probe struct {
		Swagger string `yaml:"swagger"`
	}
	_ = yaml.Unmarshal(data, &probe)

	var doc *openapi3.T
	if strings.HasPrefix(probe.Swagger, "2") {
		var generic any
		if err := yaml.Unmarshal(data, &generic); err != nil {
			return nil, loc, fmt.Errorf("parse swagger 2: %w", err)
		}
		js, err := json.Marshal(generic)
		if err != nil {
			return nil, loc, fmt.Errorf("parse swagger 2: %w", err)
		}
		var doc2 openapi2.T
		if err := json.Unmarshal(js, &doc2); err != nil {
			return nil, loc, fmt.Errorf("parse swagger 2: %w", err)
		}
		if doc, err = openapi2conv.ToV3(&doc2); err != nil {
			return nil, loc, fmt.Errorf("convert swagger 2: %w", err)
		}
		loader := openapi3.NewLoader()
		if err := loader.ResolveRefsIn(doc, loc); err != nil {
			return nil, loc, fmt.Errorf("resolve refs: %w", err)
		}
	} else {
		loader := openapi3.NewLoader()
		loader.Context = ctx
		loader.IsExternalRefsAllowed = true
		loader.ReadFromURIFunc = openapi3.URIMapCache(openapi3.ReadFromURIs(openapi3.ReadFromHTTP(cl), openapi3.ReadFromFile))
		if doc, err = loader.LoadFromDataWithPath(data, loc); err != nil {
			return nil, loc, fmt.Errorf("load openapi: %w", err)
		}
	}
	doc.InternalizeRefs(ctx, nil)
	if err := doc.Validate(ctx, openapi3.DisableExamplesValidation()); err != nil {
		fmt.Fprintf(os.Stderr, "warning: %s: spec validation: %v\n", s.Name, err)
	}
	return doc, loc, nil
}
