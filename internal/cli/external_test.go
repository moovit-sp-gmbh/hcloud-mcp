package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/moovit-sp-gmbh/hcloud-mcp/internal/server"
	"github.com/moovit-sp-gmbh/hcloud-mcp/internal/toolspec"
)

type headerRT struct{ token string }

func (h headerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if h.token != "" {
		r.Header.Set("Authorization", "Bearer "+h.token)
	}
	return http.DefaultTransport.RoundTrip(r)
}

// Two clients on one server in external mode must each reach the API with
// their own token.
func TestExternalTokenMode(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Authorization"))
		mu.Unlock()
		_, _ = w.Write([]byte(`{}`))
	}))
	defer api.Close()

	cat := &toolspec.Catalog{Tools: []toolspec.Tool{{
		Name: "svc_ping", Method: "GET", Path: "/ping", InputSchema: json.RawMessage(`{"type":"object"}`),
	}}}
	srv, err := server.New(server.Config{APIServer: api.URL, TokenMode: server.TokenModeExternal, AuthScheme: "Bearer", APIToken: "shared-must-not-be-used"}, cat)
	if err != nil {
		t.Fatal(err)
	}
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	mcpSrv := httptest.NewServer(requireToken("Authorization", h))
	defer mcpSrv.Close()

	ctx := context.Background()
	connect := func(token string) (*mcp.ClientSession, error) {
		return mcp.NewClient(&mcp.Implementation{Name: "t"}, nil).Connect(ctx, &mcp.StreamableClientTransport{
			Endpoint: mcpSrv.URL, HTTPClient: &http.Client{Transport: headerRT{token}},
		}, nil)
	}

	if _, err := connect(""); err == nil {
		t.Fatal("expected connect without token to fail")
	}
	for _, tok := range []string{"alice", "bob"} {
		cs, err := connect(tok)
		if err != nil {
			t.Fatal(err)
		}
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "svc_ping"})
		if err != nil || res.IsError {
			t.Fatalf("call as %s: %v %+v", tok, err, res)
		}
		if err := cs.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 2 || seen[0] != "Bearer alice" || seen[1] != "Bearer bob" {
		t.Fatalf("API saw %q", seen)
	}
}
