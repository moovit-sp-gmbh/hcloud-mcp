package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/moovit-sp-gmbh/hcloud-mcp/internal/toolspec"
)

const maxResponseBytes = 1 << 20 // responses larger than this are truncated

type invoker struct {
	cfg  Config
	http *http.Client
}

func (inv *invoker) call(ctx context.Context, t *toolspec.Tool, token string, args map[string]any) *mcp.CallToolResult {
	req, err := inv.buildRequest(ctx, t, token, args)
	if err != nil {
		return errorResult("%v", err)
	}
	start := time.Now()
	resp, err := inv.http.Do(req)
	if err != nil {
		inv.cfg.Logger.Warn("request failed", "tool", t.Name, "method", req.Method, "url", req.URL.Redacted(), "err", err)
		return errorResult("request to %s %s failed: %v", req.Method, req.URL.Path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return errorResult("reading response of %s %s: %v", req.Method, req.URL.Path, err)
	}
	inv.cfg.Logger.Debug("tool call", "tool", t.Name, "method", req.Method, "path", req.URL.Path, "status", resp.StatusCode, "duration", time.Since(start))

	truncated := len(body) > maxResponseBytes
	if truncated {
		body = body[:maxResponseBytes]
	}
	text := string(body)
	if truncated {
		text += "\n\n[response truncated at 1 MiB]"
	}

	if resp.StatusCode >= 400 {
		return errorResult("HTTP %s\n%s", resp.Status, text)
	}
	if strings.TrimSpace(text) == "" {
		text = "HTTP " + resp.Status
	}
	res := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
	// Structured content must be a JSON object.
	if !truncated && strings.Contains(resp.Header.Get("Content-Type"), "json") {
		var obj map[string]any
		if json.Unmarshal(body, &obj) == nil && obj != nil {
			res.StructuredContent = obj
		}
	}
	return res
}

func (inv *invoker) buildRequest(ctx context.Context, t *toolspec.Tool, token string, args map[string]any) (*http.Request, error) {
	path, query, header, err := applyParams(t, args)
	if err != nil {
		return nil, err
	}
	body, contentType, err := requestBody(t, args)
	if err != nil {
		return nil, err
	}

	u := inv.cfg.APIServer + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, t.Method, u, body)
	if err != nil {
		return nil, err
	}
	for k, vs := range header {
		req.Header[k] = vs
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json, */*;q=0.8")
	req.Header.Set("User-Agent", "hcloud-mcp/"+inv.cfg.Version)
	if inv.cfg.AuthScheme != "" {
		token = inv.cfg.AuthScheme + " " + token
	}
	req.Header.Set(inv.cfg.AuthHeader, token)
	return req, nil
}

// applyParams places the tool arguments into the path, query and headers.
func applyParams(t *toolspec.Tool, args map[string]any) (path string, query url.Values, header http.Header, err error) {
	path, query, header = t.Path, url.Values{}, http.Header{}
	for _, p := range t.Params {
		v, ok := args[p.Arg]
		if !ok || v == nil {
			if p.Required {
				return "", nil, nil, fmt.Errorf("missing required argument %q", p.Arg)
			}
			continue
		}
		switch p.In {
		case "path":
			path = strings.ReplaceAll(path, "{"+p.Name+"}", url.PathEscape(joinSimple(v)))
		case "query":
			addQuery(query, p, v)
		case "header":
			header.Set(p.Name, joinSimple(v))
		}
	}
	if strings.Contains(path, "{") {
		return "", nil, nil, fmt.Errorf("unresolved path parameters in %s", path)
	}
	return path, query, header, nil
}

// requestBody encodes the body argument, if the tool has one.
func requestBody(t *toolspec.Tool, args map[string]any) (io.Reader, string, error) {
	if t.Body == nil {
		return nil, "", nil
	}
	v, ok := args[t.Body.Arg]
	if !ok || v == nil {
		if t.Body.Required {
			return nil, "", fmt.Errorf("missing required argument %q", t.Body.Arg)
		}
		return nil, "", nil
	}
	b, ct, err := encodeBody(t.Body.ContentType, v)
	if err != nil {
		return nil, "", fmt.Errorf("encode body: %w", err)
	}
	return b, ct, nil
}

// scalar formats a JSON scalar for use in a URL or header.
func scalar(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

// joinSimple implements the OpenAPI "simple" style (comma separated).
func joinSimple(v any) string {
	switch x := v.(type) {
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = scalar(e)
		}
		return strings.Join(parts, ",")
	case map[string]any:
		keys := sortedKeys(x)
		parts := make([]string, 0, 2*len(keys))
		for _, k := range keys {
			parts = append(parts, k, scalar(x[k]))
		}
		return strings.Join(parts, ",")
	default:
		return scalar(v)
	}
}

func addQuery(q url.Values, p toolspec.Param, v any) {
	switch x := v.(type) {
	case []any:
		switch {
		case p.Explode:
			for _, e := range x {
				q.Add(p.Name, scalar(e))
			}
		case p.Style == "spaceDelimited", p.Style == "pipeDelimited":
			sep := map[string]string{"spaceDelimited": " ", "pipeDelimited": "|"}[p.Style]
			parts := make([]string, len(x))
			for i, e := range x {
				parts[i] = scalar(e)
			}
			q.Set(p.Name, strings.Join(parts, sep))
		default:
			q.Set(p.Name, joinSimple(x))
		}
	case map[string]any:
		switch {
		case p.Style == "deepObject":
			for _, k := range sortedKeys(x) {
				q.Set(p.Name+"["+k+"]", scalar(x[k]))
			}
		case p.Explode:
			for _, k := range sortedKeys(x) {
				q.Set(k, scalar(x[k]))
			}
		default:
			q.Set(p.Name, joinSimple(x))
		}
	default:
		q.Set(p.Name, scalar(v))
	}
}

func encodeBody(contentType string, v any) (io.Reader, string, error) {
	ct := strings.ToLower(contentType)
	switch {
	case strings.Contains(ct, "json"):
		// Some clients send an object/array body as a JSON string; pass it
		// through instead of encoding it a second time.
		if str, ok := v.(string); ok {
			if t := strings.TrimSpace(str); (strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[")) && json.Valid([]byte(t)) {
				return strings.NewReader(t), contentType, nil
			}
		}
		b, err := json.Marshal(v)
		return bytes.NewReader(b), contentType, err
	case ct == "application/x-www-form-urlencoded":
		obj, ok := v.(map[string]any)
		if !ok {
			return nil, "", fmt.Errorf("form body must be an object")
		}
		form := url.Values{}
		for _, k := range sortedKeys(obj) {
			if arr, ok := obj[k].([]any); ok {
				for _, e := range arr {
					form.Add(k, scalar(e))
				}
			} else {
				form.Set(k, scalar(obj[k]))
			}
		}
		return strings.NewReader(form.Encode()), contentType, nil
	case ct == "multipart/form-data":
		obj, ok := v.(map[string]any)
		if !ok {
			return nil, "", fmt.Errorf("multipart body must be an object")
		}
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		for _, k := range sortedKeys(obj) {
			if err := w.WriteField(k, scalar(obj[k])); err != nil {
				return nil, "", err
			}
		}
		if err := w.Close(); err != nil {
			return nil, "", err
		}
		return &buf, w.FormDataContentType(), nil
	default:
		return strings.NewReader(scalar(v)), contentType, nil
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
