package gen

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/moovit-sp-gmbh/hcloud-mcp/internal/toolspec"
)

const (
	maxToolNameLen    = 64 // Claude and most clients reject longer names
	maxDescriptionLen = 2000
)

var (
	invalidNameChars = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)
	invalidArgChars  = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)
)

// headers the server sets itself; never exposed as tool arguments.
var reservedHeaders = map[string]bool{
	"authorization": true, "content-type": true, "accept": true, "content-length": true,
}

// Build loads every service spec and returns the catalog.
func Build(ctx context.Context, cfg *Config) (*toolspec.Catalog, error) {
	cat := &toolspec.Catalog{GeneratedAt: time.Now().UTC().Format(time.RFC3339)}
	usedNames := map[string]bool{}
	for _, svc := range cfg.Services {
		doc, loc, err := cfg.loadSpec(ctx, svc)
		if err != nil {
			return nil, fmt.Errorf("service %q (%v): %w", svc.Name, loc, err)
		}
		if err := checkPrefix(svc.Prefix, doc.Paths.InMatchingOrder()); err != nil {
			return nil, fmt.Errorf("service %q: %w", svc.Name, err)
		}
		tools, err := buildService(svc, doc, usedNames)
		if err != nil {
			return nil, fmt.Errorf("service %q: %w", svc.Name, err)
		}
		info := toolspec.ServiceInfo{Name: svc.Name, Prefix: svc.Prefix, ToolCount: len(tools)}
		if doc.Info != nil {
			info.Title, info.APIVersion = doc.Info.Title, doc.Info.Version
		}
		cat.Services = append(cat.Services, info)
		cat.Tools = append(cat.Tools, tools...)
		fmt.Fprintf(os.Stderr, "%s: %d tools from %s\n", svc.Name, len(tools), loc)
	}
	return cat, nil
}

// checkPrefix rejects a prefix whose last segment is repeated as the first
// segment of an operation path, e.g. prefix /api/account/v1 with path
// /v1/user would call /api/account/v1/v1/user.
func checkPrefix(prefix string, paths []string) error {
	last := prefix[strings.LastIndex(prefix, "/")+1:]
	if last == "" {
		return nil
	}
	for _, p := range paths {
		first, _, _ := strings.Cut(strings.TrimPrefix(p, "/"), "/")
		if first == last {
			return fmt.Errorf("prefix %q already ends in %q and path %q starts with it, requests would go to %s%s; drop /%s from the prefix in services.yaml",
				prefix, last, p, prefix, p, last)
		}
	}
	return nil
}

func buildService(svc Service, doc *openapi3.T, usedNames map[string]bool) ([]toolspec.Tool, error) {
	var defs map[string]*openapi3.SchemaRef
	if doc.Components != nil {
		defs = doc.Components.Schemas
	}
	excluded := map[string]bool{}
	for _, e := range svc.Exclude {
		excluded[strings.ToLower(strings.TrimSpace(e))] = true
	}

	var tools []toolspec.Tool
	paths := doc.Paths.InMatchingOrder()
	sort.Strings(paths) // deterministic output
	for _, path := range paths {
		item := doc.Paths.Value(path)
		ops := item.Operations()
		methods := make([]string, 0, len(ops))
		for m := range ops {
			methods = append(methods, m)
		}
		sort.Strings(methods)
		for _, method := range methods {
			op := ops[method]
			if op.Deprecated && !svc.IncludeDeprecated {
				continue
			}
			if excluded[strings.ToLower(op.OperationID)] || excluded[strings.ToLower(method+" "+path)] {
				continue
			}
			t, err := buildTool(svc, path, method, item, op, defs)
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", method, path, err)
			}
			t.Name = uniqueName(t.Name, usedNames)
			tools = append(tools, *t)
		}
	}
	return tools, nil
}

func buildTool(svc Service, path, method string, item *openapi3.PathItem, op *openapi3.Operation, defs map[string]*openapi3.SchemaRef) (*toolspec.Tool, error) {
	base := op.OperationID
	if base == "" {
		base = strings.ToLower(method) + "_" + path
	}
	t := &toolspec.Tool{
		Name:        svc.Name + "_" + base,
		Title:       op.Summary,
		Description: describe(method, svc.Prefix+path, op),
		Service:     svc.Name,
		Method:      method,
		Path:        svc.Prefix + path,
		ReadOnly:    method == http.MethodGet || method == http.MethodHead,
		Destructive: method == http.MethodDelete,
		Idempotent:  method == http.MethodGet || method == http.MethodHead || method == http.MethodPut || method == http.MethodDelete,
	}

	in := &toolInput{sb: newSchemaBuilder(defs), props: map[string]any{}, usedArgs: map[string]bool{}}
	if op.RequestBody != nil {
		in.usedArgs["body"] = true // reserve before parameters claim names
	}
	for _, p := range mergedParams(item, op) {
		if exposedParam(p) {
			t.Params = append(t.Params, in.addParam(p))
		}
	}
	if op.RequestBody != nil && op.RequestBody.Value != nil {
		t.Body = in.addBody(op.RequestBody.Value)
	}

	input := map[string]any{"type": "object", "properties": in.props}
	if len(in.required) > 0 {
		input["required"] = in.required
	}
	if d := in.sb.defsJSON(); len(d) > 0 {
		input["$defs"] = d
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	t.InputSchema = raw
	return t, nil
}

// toolInput accumulates the input schema of one tool.
type toolInput struct {
	sb       *schemaBuilder
	props    map[string]any
	required []string
	usedArgs map[string]bool
}

// mergedParams returns path-item and operation parameters; operation
// parameters override path-item parameters with the same name and location.
func mergedParams(item *openapi3.PathItem, op *openapi3.Operation) []*openapi3.Parameter {
	byKey := map[string]int{}
	var out []*openapi3.Parameter
	for _, list := range []openapi3.Parameters{item.Parameters, op.Parameters} {
		for _, pr := range list {
			if pr == nil || pr.Value == nil {
				continue
			}
			key := pr.Value.In + ":" + pr.Value.Name
			if i, ok := byKey[key]; ok {
				out[i] = pr.Value
				continue
			}
			byKey[key] = len(out)
			out = append(out, pr.Value)
		}
	}
	return out
}

// exposedParam reports whether p becomes a tool argument. Cookies and
// headers the server sets itself are skipped.
func exposedParam(p *openapi3.Parameter) bool {
	switch p.In {
	case openapi3.ParameterInPath, openapi3.ParameterInQuery:
		return true
	case openapi3.ParameterInHeader:
		return !reservedHeaders[strings.ToLower(p.Name)]
	default:
		return false
	}
}

func (in *toolInput) addParam(p *openapi3.Parameter) toolspec.Param {
	arg := argName(p.Name, p.In, in.usedArgs)
	schema := in.paramSchema(p)
	if p.Description != "" {
		schema["description"] = p.Description
	}
	if arg != p.Name {
		desc, _ := schema["description"].(string)
		schema["description"] = strings.TrimSpace(fmt.Sprintf("%s (%s parameter %q)", desc, p.In, p.Name))
	}
	required := p.Required || p.In == openapi3.ParameterInPath
	in.props[arg] = schema
	in.required = appendIf(in.required, arg, required)
	style, explode := paramStyle(p)
	return toolspec.Param{Arg: arg, Name: p.Name, In: p.In, Style: style, Explode: explode, Required: required}
}

func (in *toolInput) paramSchema(p *openapi3.Parameter) map[string]any {
	if p.Schema != nil {
		return in.sb.convert(p.Schema)
	}
	// content-encoded parameters: take the first media type's schema
	for _, mt := range p.Content {
		if mt.Schema != nil {
			return in.sb.convert(mt.Schema)
		}
	}
	return map[string]any{"type": "string"}
}

// paramStyle applies the OpenAPI defaults: simple for path and header, form
// for query; explode defaults to true only for form.
func paramStyle(p *openapi3.Parameter) (style string, explode bool) {
	style = p.Style
	if style == "" {
		style = map[string]string{"path": "simple", "query": "form", "header": "simple"}[p.In]
	}
	if p.Explode != nil {
		return style, *p.Explode
	}
	return style, style == "form"
}

func (in *toolInput) addBody(rb *openapi3.RequestBody) *toolspec.Body {
	ct, mt := pickContentType(rb.Content)
	if ct == "" {
		return nil
	}
	schema := map[string]any{}
	if mt != nil && mt.Schema != nil {
		sr := mt.Schema
		if sr.Ref != "" && sr.Value != nil {
			// Inline the top-level component: many MCP clients do not follow
			// $ref and would otherwise not know the body is an object (and
			// send it as a JSON string). Nested refs stay in $defs.
			sr = &openapi3.SchemaRef{Value: sr.Value}
		}
		schema = in.sb.convert(sr)
	}
	if _, has := schema["description"]; !has {
		desc := rb.Description
		if desc == "" {
			desc = "Request body (" + ct + ")"
		}
		schema["description"] = desc
	}
	in.props["body"] = schema
	in.required = appendIf(in.required, "body", rb.Required)
	return &toolspec.Body{Arg: "body", ContentType: ct, Required: rb.Required}
}

func describe(method, path string, op *openapi3.Operation) string {
	var parts []string
	if op.Summary != "" {
		parts = append(parts, op.Summary)
	}
	if op.Description != "" && op.Description != op.Summary {
		parts = append(parts, op.Description)
	}
	d := strings.Join(parts, "\n\n")
	if len(d) > maxDescriptionLen {
		d = d[:maxDescriptionLen] + "…"
	}
	if d != "" {
		d += "\n\n"
	}
	return d + "HTTP " + method + " " + path
}

// pickContentType prefers JSON, then form encodings, then text.
func pickContentType(content openapi3.Content) (string, *openapi3.MediaType) {
	if len(content) == 0 {
		return "", nil
	}
	keys := make([]string, 0, len(content))
	for k := range content {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rank := func(ct string) int {
		ct = strings.ToLower(ct)
		switch {
		case ct == "application/json":
			return 0
		case strings.HasSuffix(ct, "+json") || strings.Contains(ct, "json"):
			return 1
		case ct == "application/x-www-form-urlencoded":
			return 2
		case ct == "multipart/form-data":
			return 3
		case strings.HasPrefix(ct, "text/"):
			return 4
		default:
			return 5
		}
	}
	sort.SliceStable(keys, func(i, j int) bool { return rank(keys[i]) < rank(keys[j]) })
	return keys[0], content[keys[0]]
}

func argName(name, in string, used map[string]bool) string {
	arg := invalidArgChars.ReplaceAllString(name, "_")
	if arg == "" {
		arg = in
	}
	if len(arg) > 60 {
		arg = arg[:60]
	}
	if used[arg] {
		arg = arg + "_" + in
	}
	for i := 2; used[arg]; i++ {
		arg = fmt.Sprintf("%s_%d", arg, i)
	}
	used[arg] = true
	return arg
}

func uniqueName(name string, used map[string]bool) string {
	name = strings.Trim(invalidNameChars.ReplaceAllString(name, "_"), "_")
	if len(name) > maxToolNameLen {
		sum := sha1.Sum([]byte(name))
		name = name[:maxToolNameLen-9] + "_" + hex.EncodeToString(sum[:])[:8]
	}
	base := name
	for i := 2; used[name]; i++ {
		suffix := fmt.Sprintf("_%d", i)
		if len(base)+len(suffix) > maxToolNameLen {
			name = base[:maxToolNameLen-len(suffix)] + suffix
		} else {
			name = base + suffix
		}
	}
	used[name] = true
	return name
}

func appendIf(list []string, v string, cond bool) []string {
	if cond && !slices.Contains(list, v) {
		return append(list, v)
	}
	return list
}
