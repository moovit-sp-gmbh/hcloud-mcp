// Command gen reads services.yaml, loads every OpenAPI document and writes the
// tool catalog that is embedded into the hcloud-mcp binary.
//
//	go run ./cmd/gen -config services.yaml -out internal/catalog/generated/catalog.json
//
// Environment:
//
//	SPEC_BASE_URL  base URL for specs that are not local files
//	               (default: specBaseUrl from services.yaml, then HCLOUD_API_SERVER)
//	SPEC_TOKEN     bearer token sent when downloading specs (default: HCLOUD_API_TOKEN)
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/moovit-sp-gmbh/hcloud-mcp/internal/gen"
)

func main() {
	cfgPath := flag.String("config", "services.yaml", "path to services.yaml")
	out := flag.String("out", "internal/catalog/generated/catalog.json", "output file")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := run(ctx, *cfgPath, *out); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfgPath, out string) error {
	cfg, err := gen.LoadConfig(cfgPath)
	if err != nil {
		return err
	}
	cat, err := gen.Build(ctx, cfg)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(cat, "", "  ")
	if err != nil {
		return err
	}
	tmp := out + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, out); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %d tools from %d services to %s\n", len(cat.Tools), len(cat.Services), out)
	return nil
}
