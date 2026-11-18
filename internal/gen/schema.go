package gen

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

const componentPrefix = "#/components/schemas/"

// schemaBuilder converts OpenAPI schemas into JSON Schema (2020-12) maps for a
// single tool. Component refs are rewritten to #/$defs/... and every referenced
// component is collected into the tool's $defs, so recursive schemas work and
// shared schemas are not duplicated within one tool.
type schemaBuilder struct {
	components map[string]*openapi3.SchemaRef
	pending    []string
	needed     map[string]bool
	defs       map[string]any
}

func newSchemaBuilder(components map[string]*openapi3.SchemaRef) *schemaBuilder {
	return &schemaBuilder{components: components, needed: map[string]bool{}, defs: map[string]any{}}
}

func (b *schemaBuilder) convert(sr *openapi3.SchemaRef) map[string]any {
	raw, err := json.Marshal(sr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: cannot marshal schema: %v\n", err)
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return map[string]any{}
	}
	b.clean(m)
	return m
}

// defsJSON resolves all component schemas referenced so far (transitively).
func (b *schemaBuilder) defsJSON() map[string]any {
	for len(b.pending) > 0 {
		name := b.pending[0]
		b.pending = b.pending[1:]
		sr, ok := b.components[name]
		if !ok || sr == nil || sr.Value == nil {
			fmt.Fprintf(os.Stderr, "warning: unresolved schema component %q\n", name)
			b.defs[name] = map[string]any{}
			continue
		}
		b.defs[name] = b.convert(&openapi3.SchemaRef{Value: sr.Value})
	}
	return b.defs
}

func (b *schemaBuilder) need(name string) {
	if !b.needed[name] {
		b.needed[name] = true
		b.pending = append(b.pending, name)
	}
}

var (
	schemaMapKeywords    = []string{"properties", "patternProperties", "$defs", "dependentSchemas"}
	schemaSingleKeywords = []string{"items", "not", "additionalProperties", "if", "then", "else", "contains", "propertyNames", "unevaluatedItems", "unevaluatedProperties"}
	schemaListKeywords   = []string{"allOf", "anyOf", "oneOf", "prefixItems"}
)

// clean normalizes one schema object in place and recurses into sub-schemas.
func (b *schemaBuilder) clean(v any) {
	m, ok := v.(map[string]any)
	if !ok {
		return // boolean schema
	}
	dropUnsupported(m)
	b.rewriteRef(m)
	convertNullable(m)
	convertExclusiveBounds(m)
	b.cleanChildren(m)
}

// dropUnsupported removes vendor extensions and OpenAPI-only keywords.
func dropUnsupported(m map[string]any) {
	for k := range m {
		if strings.HasPrefix(k, "x-") {
			delete(m, k)
		}
	}
	delete(m, "xml")
	delete(m, "discriminator") // mapping points at components, not useful for clients
	delete(m, "externalDocs")
}

// rewriteRef points component refs at #/$defs and records them as needed.
func (b *schemaBuilder) rewriteRef(m map[string]any) {
	ref, ok := m["$ref"].(string)
	if !ok {
		return
	}
	rest, found := strings.CutPrefix(ref, componentPrefix)
	if !found {
		delete(m, "$ref") // unknown ref target: degrade to "any"
		return
	}
	name, _, _ := strings.Cut(rest, "/")
	b.need(strings.NewReplacer("~1", "/", "~0", "~").Replace(name))
	m["$ref"] = "#/$defs/" + rest
}

// convertNullable turns OpenAPI 3.0 nullable into a JSON Schema type union.
func convertNullable(m map[string]any) {
	if n, _ := m["nullable"].(bool); n {
		switch t := m["type"].(type) {
		case string:
			m["type"] = []any{t, "null"}
		case []any:
			if !slices.Contains(t, any("null")) {
				m["type"] = append(t, "null")
			}
		}
	}
	delete(m, "nullable")
}

// convertExclusiveBounds turns OpenAPI 3.0 boolean exclusiveMinimum/Maximum
// into the numeric form of JSON Schema 2020-12.
func convertExclusiveBounds(m map[string]any) {
	for _, pair := range [][2]string{{"exclusiveMinimum", "minimum"}, {"exclusiveMaximum", "maximum"}} {
		ex, ok := m[pair[0]].(bool)
		if !ok {
			continue
		}
		delete(m, pair[0])
		if bound, has := m[pair[1]]; ex && has {
			m[pair[0]] = bound
			delete(m, pair[1])
		}
	}
}

func (b *schemaBuilder) cleanChildren(m map[string]any) {
	for _, k := range schemaMapKeywords {
		sub, ok := m[k].(map[string]any)
		if !ok {
			continue
		}
		if k == "properties" {
			dropReadOnly(m, sub)
		}
		for _, s := range sub {
			b.clean(s)
		}
	}
	for _, k := range schemaSingleKeywords {
		if list, isList := m[k].([]any); isList { // legacy tuple "items"
			b.cleanAll(list)
		} else if s, ok := m[k]; ok {
			b.clean(s)
		}
	}
	for _, k := range schemaListKeywords {
		if list, ok := m[k].([]any); ok {
			b.cleanAll(list)
		}
	}
}

func (b *schemaBuilder) cleanAll(list []any) {
	for _, e := range list {
		b.clean(e)
	}
}

// dropReadOnly removes server-generated properties: the model should never
// be asked to provide them.
func dropReadOnly(m, props map[string]any) {
	for name, s := range props {
		if sm, ok := s.(map[string]any); ok && sm["readOnly"] == true {
			delete(props, name)
			if req, ok := m["required"].([]any); ok {
				m["required"] = slices.DeleteFunc(req, func(r any) bool { return r == name })
			}
		}
	}
	if req, ok := m["required"].([]any); ok && len(req) == 0 {
		delete(m, "required")
	}
}
