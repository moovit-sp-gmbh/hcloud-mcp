package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/moovit-sp-gmbh/hcloud-mcp/internal/toolspec"
)

func TestToolCallsReachAPI(t *testing.T) {
	type seen struct{ method, uri, auth, ctype, body string }
	var got seen
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = seen{r.Method, r.URL.RequestURI(), r.Header.Get("Authorization"), r.Header.Get("Content-Type"), string(b)}
		if strings.HasSuffix(r.URL.Path, "/missing") {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer api.Close()

	cat := &toolspec.Catalog{Tools: []toolspec.Tool{
		{
			Name: "svc_update", Method: "PUT", Path: "/api/svc/v1/users/{id}",
			Params: []toolspec.Param{
				{Arg: "id", Name: "id", In: "path", Required: true},
				{Arg: "tags", Name: "tags", In: "query", Style: "form", Explode: true},
				{Arg: "filter", Name: "filter", In: "query", Style: "deepObject", Explode: true},
			},
			Body:        &toolspec.Body{Arg: "body", ContentType: "application/json", Required: true},
			InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"body":{"type":"object"}}}`),
		},
		{
			Name: "svc_get", Method: "GET", Path: "/api/svc/v1/{what}",
			Params:      []toolspec.Param{{Arg: "what", Name: "what", In: "path", Required: true}},
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
	}}

	srv, err := New(Config{APIServer: api.URL + "/", APIToken: "secret", AuthScheme: "Bearer", Version: "test"}, cat)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 2 {
		t.Fatalf("ListTools: %v %+v", err, tools)
	}

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "svc_update", Arguments: map[string]any{
		"id": "a b", "tags": []string{"x", "y"}, "filter": map[string]any{"age": 42},
		"body": map[string]any{"name": "Ann"},
	}})
	if err != nil || res.IsError {
		t.Fatalf("CallTool: %v %+v", err, res)
	}
	want := seen{"PUT", "/api/svc/v1/users/a%20b?filter%5Bage%5D=42&tags=x&tags=y", "Bearer secret", "application/json", `{"name":"Ann"}`}
	if got != want {
		t.Fatalf("request mismatch\n got  %+v\n want %+v", got, want)
	}
	if text := res.Content[0].(*mcp.TextContent).Text; text != `{"ok":true}` {
		t.Fatalf("unexpected result %q", text)
	}

	// A body sent as a JSON string (some clients do this) is not encoded twice.
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "svc_update", Arguments: map[string]any{
		"id": "1", "body": ` {"filters": []}`,
	}})
	if err != nil || res.IsError {
		t.Fatalf("CallTool: %v %+v", err, res)
	}
	if got.body != `{"filters": []}` {
		t.Fatalf("string body re-encoded: %q", got.body)
	}

	// Missing required body is rejected before any request.
	res, _ = cs.CallTool(ctx, &mcp.CallToolParams{Name: "svc_update", Arguments: map[string]any{"id": "1"}})
	if !res.IsError {
		t.Fatal("expected error for missing body")
	}

	// HTTP errors become tool errors including the status.
	res, _ = cs.CallTool(ctx, &mcp.CallToolParams{Name: "svc_get", Arguments: map[string]any{"what": "missing"}})
	if !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "404") {
		t.Fatalf("expected 404 tool error, got %+v", res.Content[0])
	}
}

func TestConfigValidation(t *testing.T) {
	for _, c := range []Config{{}, {APIServer: "ftp://x", APIToken: "t"}, {APIServer: "https://x"}} {
		if _, err := New(c, &toolspec.Catalog{}); err == nil {
			t.Errorf("expected error for %+v", c)
		}
	}
}
