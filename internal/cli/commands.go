package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/moovit-sp-gmbh/hcloud-mcp/internal/catalog"
	"github.com/moovit-sp-gmbh/hcloud-mcp/internal/server"
)

func newStdioCmd(opts *options, version string) *cobra.Command {
	return &cobra.Command{
		Use:   "stdio",
		Short: "Run the MCP server over stdin/stdout",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if opts.tokenMode == server.TokenModeExternal {
				return fmt.Errorf("token mode %q needs per-request headers and only works with the http command; use --token-mode env with stdio", server.TokenModeExternal)
			}
			log := opts.logger()
			srv, err := opts.newServer(version, log)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			log.Info("serving MCP over stdio", "apiServer", opts.apiServer)
			if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
				return err
			}
			return nil
		},
	}
}

func newHTTPCmd(opts *options, version string) *cobra.Command {
	var (
		addr      string
		path      string
		stateless bool
	)
	cmd := &cobra.Command{
		Use:   "http",
		Short: "Run the MCP server over streamable HTTP",
		RunE: func(cmd *cobra.Command, _ []string) error {
			log := opts.logger()
			srv, err := opts.newServer(version, log)
			if err != nil {
				return err
			}
			handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv },
				&mcp.StreamableHTTPOptions{Stateless: stateless, Logger: log})

			var endpoint http.Handler = handler
			if opts.tokenMode == server.TokenModeExternal {
				endpoint = requireToken(opts.tokenHdr, handler)
			}
			mux := http.NewServeMux()
			mux.Handle(path, endpoint)
			mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("ok\n"))
			})
			hs := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			errc := make(chan error, 1)
			go func() { errc <- hs.ListenAndServe() }()
			log.Info("serving MCP over streamable HTTP", "addr", addr, "path", path, "apiServer", opts.apiServer, "tokenMode", opts.tokenMode)

			select {
			case err := <-errc:
				return err
			case <-ctx.Done():
			}
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return hs.Shutdown(shutdownCtx)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", envOr(":8080", "ADDR"), "listen address (env ADDR)")
	cmd.Flags().StringVar(&path, "path", "/mcp", "HTTP path of the MCP endpoint")
	cmd.Flags().BoolVar(&stateless, "stateless", false, "run without MCP sessions (easier behind load balancers)")
	return cmd
}

// requireToken rejects MCP requests without a client token with 401, so
// clients learn about missing credentials at connect time rather than on the
// first tool call.
func requireToken(header string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if server.ExtractToken(r.Header, header) == "" {
			if http.CanonicalHeaderKey(header) == "Authorization" {
				w.Header().Set("WWW-Authenticate", `Bearer realm="hcloud-mcp"`)
			}
			http.Error(w, "missing API token in "+header+" header", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// newHealthcheckCmd probes the /healthz endpoint of a running `http` server.
// It exists for container HEALTHCHECKs in images without a shell or curl.
func newHealthcheckCmd() *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:    "healthcheck",
		Short:  "Exit 0 if the local http server answers /healthz",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return fmt.Errorf("invalid address %q: %w", addr, err)
			}
			if host == "" || host == "0.0.0.0" || host == "::" {
				host = "127.0.0.1"
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 3*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(host, port)+"/healthz", nil)
			if err != nil {
				return err
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("healthz: %s", resp.Status)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&addr, "addr", envOr(":8080", "ADDR"), "address of the http server (env ADDR)")
	return cmd
}

func newToolsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tools",
		Short: "List the services and tools compiled into this binary",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cat, err := catalog.Get()
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			p := &printer{w: w}
			p.printf("generated %s\n\n", cat.GeneratedAt)
			p.printf("SERVICE\tPREFIX\tAPI\tVERSION\tTOOLS\n")
			for _, s := range cat.Services {
				p.printf("%s\t%s\t%s\t%s\t%d\n", s.Name, s.Prefix, s.Title, s.APIVersion, s.ToolCount)
			}
			p.printf("\nTOOL\tMETHOD\tPATH\n")
			for _, t := range cat.Tools {
				p.printf("%s\t%s\t%s\n", t.Name, t.Method, t.Path)
			}
			if p.err != nil {
				return p.err
			}
			return w.Flush()
		},
	}
}

// printer remembers the first write error so output code stays linear.
type printer struct {
	w   io.Writer
	err error
}

func (p *printer) printf(format string, a ...any) {
	if p.err == nil {
		_, p.err = fmt.Fprintf(p.w, format, a...)
	}
}
