// Package catalog embeds the tool catalog generated from the OpenAPI specs
// listed in services.yaml. The catalog is a build artifact (gitignored);
// create it with `make generate` (or `go generate ./...`) before building.
package catalog

//go:generate go run ../../cmd/gen -config ../../services.yaml -out generated/catalog.json

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sync"

	"github.com/moovit-sp-gmbh/hcloud-mcp/internal/toolspec"
)

// The directory always exists (it holds a .gitkeep), so the package compiles
// on a fresh checkout; the catalog itself may be missing.
//
//go:embed all:generated
var files embed.FS

const path = "generated/catalog.json"

var load = sync.OnceValues(func() (*toolspec.Catalog, error) {
	raw, err := fs.ReadFile(files, path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errors.New("this binary was built without a tool catalog; run `make generate` before building")
	}
	if err != nil {
		return nil, err
	}
	var c toolspec.Catalog
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("embedded catalog is invalid: %w", err)
	}
	return &c, nil
})

// Get returns the embedded catalog.
func Get() (*toolspec.Catalog, error) { return load() }
