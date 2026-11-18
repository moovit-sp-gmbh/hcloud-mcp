// Package cli contains the cobra commands of hcloud-mcp.
package cli

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/moovit-sp-gmbh/hcloud-mcp/internal/catalog"
	"github.com/moovit-sp-gmbh/hcloud-mcp/internal/server"
)

type options struct {
	apiServer  string
	apiToken   string
	tokenMode  string
	tokenHdr   string
	authHeader string
	authScheme string
	timeout    time.Duration
	logLevel   string
}

// env returns the first non-empty environment variable of names.
func env(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

func envOr(def string, names ...string) string {
	if v := env(names...); v != "" {
		return v
	}
	return def
}

// Execute runs the root command.
func Execute(version string) error {
	opts := &options{}
	root := &cobra.Command{
		Use:   "hcloud-mcp",
		Short: "MCP server for the HCloud API, generated from its OpenAPI specs",
		Long: `hcloud-mcp exposes the HCloud REST API as MCP tools.

Configuration (flags override environment variables):
  HCLOUD_API_SERVER  base URL of the HCloud API   (flag: --api-server)
  HCLOUD_API_TOKEN   token used to authenticate   (flag: --api-token)

Token modes (--token-mode, env apiTokenMode):
  env       every API call uses HCLOUD_API_TOKEN (default; required for stdio)
  external  http only: every API call uses the token the MCP client sends
            with its request (default header: Authorization: Bearer <token>).
            HCLOUD_API_TOKEN is ignored, so each user acts with their own credentials.`,
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	f := root.PersistentFlags()
	f.StringVar(&opts.apiServer, "api-server", env("HCLOUD_API_SERVER"), "HCloud API base URL (env HCLOUD_API_SERVER)")
	f.StringVar(&opts.apiToken, "api-token", "", "HCloud API token (env HCLOUD_API_TOKEN; prefer the env var, flags are visible in process lists)")
	f.StringVar(&opts.tokenMode, "token-mode", envOr(server.TokenModeEnv, "apiTokenMode", "API_TOKEN_MODE"), "where the API token comes from: env or external (env apiTokenMode)")
	f.StringVar(&opts.tokenHdr, "token-header", envOr("Authorization", "apiTokenHeader", "API_TOKEN_HEADER"), "incoming MCP request header carrying the client token in external mode (env apiTokenHeader)")
	f.StringVar(&opts.authHeader, "auth-header", envOr("Authorization", "apiAuthHeader", "API_AUTH_HEADER"), "header that carries the token (env apiAuthHeader)")
	f.StringVar(&opts.authScheme, "auth-scheme", envOr("Bearer", "apiAuthScheme", "API_AUTH_SCHEME"), "token scheme prefix, empty for raw token (env apiAuthScheme)")
	f.DurationVar(&opts.timeout, "timeout", 60*time.Second, "timeout for a single API request")
	f.StringVar(&opts.logLevel, "log-level", envOr("info", "LOG_LEVEL"), "log level: debug, info, warn, error")

	root.AddCommand(newStdioCmd(opts, version), newHTTPCmd(opts, version), newToolsCmd(), newHealthcheckCmd())
	return root.Execute()
}

// logger always writes to stderr: stdout belongs to the stdio transport.
func (o *options) logger() *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(o.logLevel)); err != nil {
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}

func (o *options) newServer(version string, log *slog.Logger) (*mcp.Server, error) {
	cat, err := catalog.Get()
	if err != nil {
		return nil, err
	}
	token := o.apiToken
	if token == "" {
		token = env("HCLOUD_API_TOKEN")
	}
	if o.tokenMode == server.TokenModeExternal && token != "" {
		log.Warn("HCLOUD_API_TOKEN is ignored in external token mode")
	}
	srv, err := server.New(server.Config{
		APIServer:   strings.TrimSpace(o.apiServer),
		APIToken:    strings.TrimSpace(token),
		TokenMode:   o.tokenMode,
		TokenHeader: o.tokenHdr,
		AuthHeader:  o.authHeader,
		AuthScheme:  o.authScheme,
		Timeout:     o.timeout,
		Version:     version,
		Logger:      log,
	}, cat)
	if err != nil {
		return nil, fmt.Errorf("%w (see hcloud-mcp --help)", err)
	}
	return srv, nil
}
