// Command hcloud-mcp is an MCP server for the HCloud API.
package main

import (
	"fmt"
	"os"

	"github.com/moovit-sp-gmbh/hcloud-mcp/internal/cli"
)

// version is set at build time: -ldflags "-X main.version=1.2.3"
var version = "0.0.0-dev"

func main() {
	if err := cli.Execute(version); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
