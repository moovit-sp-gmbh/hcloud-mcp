// Package server builds the MCP server from the embedded tool catalog and
// forwards tool calls as HTTP requests to the helmut.cloud API server.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/moovit-sp-gmbh/hcloud-mcp/internal/toolspec"
)

// Token modes.
const (
	// TokenModeEnv uses the configured APIToken for every call.
	TokenModeEnv = "env"
	// TokenModeExternal uses the token that the MCP client sends with each
	// HTTP request (streamable HTTP only). APIToken is ignored.
	TokenModeExternal = "external"
)

// Config is the runtime configuration.
type Config struct {
	APIServer string // base URL, e.g. https://api.example.com
	APIToken  string // used in TokenModeEnv
	TokenMode string // TokenModeEnv (default) or TokenModeExternal
	// TokenHeader is the incoming MCP request header that carries the
	// client's token in TokenModeExternal. Default "Authorization"; a
	// "Bearer " prefix is stripped.
	TokenHeader string
	AuthHeader  string // outgoing header to the API, default "Authorization"
	AuthScheme  string // default "Bearer"; empty sends the raw token
	Timeout     time.Duration
	Version     string
	Logger      *slog.Logger
}

func (c *Config) validate() error {
	if c.APIServer == "" {
		return fmt.Errorf("HCLOUD_API_SERVER is not set")
	}
	u, err := url.Parse(c.APIServer)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("HCLOUD_API_SERVER %q is not a valid http(s) URL", c.APIServer)
	}
	switch c.TokenMode {
	case "", TokenModeEnv:
		c.TokenMode = TokenModeEnv
		if c.APIToken == "" {
			return fmt.Errorf("HCLOUD_API_TOKEN is not set (required in token mode %q)", TokenModeEnv)
		}
	case TokenModeExternal:
		c.APIToken = "" // never fall back to a shared token
		if c.TokenHeader == "" {
			c.TokenHeader = "Authorization"
		}
	default:
		return fmt.Errorf("invalid token mode %q (want %q or %q)", c.TokenMode, TokenModeEnv, TokenModeExternal)
	}
	c.APIServer = strings.TrimRight(c.APIServer, "/")
	if c.AuthHeader == "" {
		c.AuthHeader = "Authorization"
	}
	if c.Timeout <= 0 {
		c.Timeout = 60 * time.Second
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	return nil
}

// New creates an MCP server exposing every tool of the catalog.
func New(cfg Config, cat *toolspec.Catalog) (*mcp.Server, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	inv := &invoker{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout}}

	var services []string
	for _, s := range cat.Services {
		services = append(services, fmt.Sprintf("%s (%s %s)", s.Name, s.Title, s.APIVersion))
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "hcloud-mcp", Title: "helmut.cloud", Version: cfg.Version}, &mcp.ServerOptions{
		Logger: cfg.Logger,
		Instructions: "Tools map 1:1 to helmut.cloud REST API operations; tool names are prefixed with the microservice name. " +
			"Path, query and header parameters are top-level arguments, the request body goes into the \"body\" argument. " +
			"Services: " + strings.Join(services, ", ") + ".",
	})

	for i := range cat.Tools {
		t := &cat.Tools[i]
		destructive := t.Destructive
		openWorld := true
		srv.AddTool(&mcp.Tool{
			Name:        t.Name,
			Title:       t.Title,
			Description: t.Description,
			InputSchema: t.InputSchema,
			Annotations: &mcp.ToolAnnotations{
				Title:           t.Title,
				ReadOnlyHint:    t.ReadOnly,
				DestructiveHint: &destructive,
				IdempotentHint:  t.Idempotent,
				OpenWorldHint:   &openWorld,
			},
		}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args map[string]any
			if len(req.Params.Arguments) > 0 {
				dec := json.NewDecoder(strings.NewReader(string(req.Params.Arguments)))
				dec.UseNumber()
				if err := dec.Decode(&args); err != nil {
					return errorResult("invalid arguments: %v", err), nil
				}
			}
			token, err := cfg.tokenFor(req)
			if err != nil {
				return errorResult("%v", err), nil
			}
			return inv.call(ctx, t, token, args), nil
		})
	}
	return srv, nil
}

// tokenFor returns the API token to use for one tool call.
func (c *Config) tokenFor(req *mcp.CallToolRequest) (string, error) {
	if c.TokenMode != TokenModeExternal {
		return c.APIToken, nil
	}
	var h http.Header
	if req.Extra != nil {
		h = req.Extra.Header
	}
	if t := ExtractToken(h, c.TokenHeader); t != "" {
		return t, nil
	}
	return "", fmt.Errorf("no API token: send it in the %q header of the MCP HTTP request", c.TokenHeader)
}

// ExtractToken reads a token from header name, stripping a "Bearer " prefix.
func ExtractToken(h http.Header, name string) string {
	v := strings.TrimSpace(h.Get(name))
	if len(v) > 7 && strings.EqualFold(v[:7], "bearer ") {
		v = strings.TrimSpace(v[7:])
	}
	return v
}

func errorResult(format string, a ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, a...)}},
	}
}
