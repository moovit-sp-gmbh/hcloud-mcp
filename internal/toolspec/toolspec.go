// Package toolspec defines the build-time generated description of every MCP
// tool. The generator (cmd/gen) writes it as JSON, the binary embeds it
// (internal/catalog) and the server (internal/server) turns it into MCP tools.
package toolspec

import "encoding/json"

// Catalog is the root document written by the generator.
type Catalog struct {
	GeneratedAt string        `json:"generatedAt"`
	Services    []ServiceInfo `json:"services"`
	Tools       []Tool        `json:"tools"`
}

// ServiceInfo describes one microservice the tools were generated from.
type ServiceInfo struct {
	Name       string `json:"name"`
	Prefix     string `json:"prefix"`
	Title      string `json:"title,omitempty"`
	APIVersion string `json:"apiVersion,omitempty"`
	ToolCount  int    `json:"toolCount"`
}

// Tool is one OpenAPI operation exposed as an MCP tool.
type Tool struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description"`
	Service     string `json:"service"`
	Method      string `json:"method"`
	// Path is the request path relative to HCLOUD_API_SERVER, including the service
	// prefix, with OpenAPI {placeholders} for path parameters.
	Path        string          `json:"path"`
	Params      []Param         `json:"params,omitempty"`
	Body        *Body           `json:"body,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
	ReadOnly    bool            `json:"readOnly,omitempty"`
	Destructive bool            `json:"destructive,omitempty"`
	Idempotent  bool            `json:"idempotent,omitempty"`
}

// Param maps a tool argument to an HTTP parameter.
type Param struct {
	Arg      string `json:"arg"`  // property name in the tool's input schema
	Name     string `json:"name"` // parameter name on the wire
	In       string `json:"in"`   // path | query | header
	Style    string `json:"style,omitempty"`
	Explode  bool   `json:"explode,omitempty"`
	Required bool   `json:"required,omitempty"`
}

// Body describes the request body of a tool.
type Body struct {
	Arg         string `json:"arg"`
	ContentType string `json:"contentType"`
	Required    bool   `json:"required,omitempty"`
}
