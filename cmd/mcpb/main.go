// Command mcpb packages the cross-compiled hcloud-mcp binaries into MCP
// Bundles (.mcpb) for Claude Desktop and other MCPB-aware clients.
//
// It looks for binaries in -dist, in either layout:
//
//	<dist>/<name>_<os>_<arch>[.exe]           (make build)
//	<dist>/<build-id>_<os>_<arch>_<variant>/<name>[.exe]   (goreleaser)
//
// and writes into -out:
//
//   - <name>_<version>_<os>_<arch>.mcpb  one bundle per binary
//   - <name>_<version>.mcpb              one universal bundle: macOS universal
//     (amd64+arm64), Windows amd64 (also runs on Windows arm64) and Linux amd64
package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/moovit-sp-gmbh/hcloud-mcp/internal/catalog"
)

const name = "hcloud-mcp"

var osToPlatform = map[string]string{"darwin": "darwin", "windows": "win32", "linux": "linux"}

func main() {
	version := flag.String("version", "0.0.0-dev", "bundle version (semver)")
	dist := flag.String("dist", "build", "directory with the compiled binaries")
	out := flag.String("out", "", "directory for the bundles (default: -dist)")
	flag.Parse()
	if *out == "" {
		*out = *dist
	}
	if err := run(strings.TrimPrefix(*version, "v"), *dist, *out); err != nil {
		fmt.Fprintln(os.Stderr, "mcpb:", err)
		os.Exit(1)
	}
}

type file struct {
	path string
	data []byte
	exec bool
}

// findBinaries returns "os_arch" -> binary for every binary found in dist.
func findBinaries(dist string) (map[string][]byte, error) {
	bins := map[string][]byte{}
	for goos := range osToPlatform {
		for _, arch := range []string{"amd64", "arm64"} {
			candidates := []string{filepath.Join(dist, binName(goos, arch))}
			if m, err := filepath.Glob(filepath.Join(dist, "*_"+goos+"_"+arch+"*", exe(name, goos))); err == nil {
				candidates = append(candidates, m...)
			}
			for _, p := range candidates {
				data, err := os.ReadFile(p)
				if err == nil {
					bins[goos+"_"+arch] = data
					break
				}
				if !os.IsNotExist(err) {
					return nil, err
				}
			}
		}
	}
	if len(bins) == 0 {
		return nil, fmt.Errorf("no binaries found in %s (run `make build` first)", dist)
	}
	return bins, nil
}

func run(version, dist, outDir string) error {
	bins, err := findBinaries(dist)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	// Per-platform bundles.
	for key, data := range bins {
		goos, arch, _ := strings.Cut(key, "_")
		entry := "server/" + exe(name, goos)
		m := manifest(version, "${__dirname}/"+entry, []string{osToPlatform[goos]}, nil)
		out := filepath.Join(outDir, fmt.Sprintf("%s_%s_%s_%s.mcpb", name, version, goos, arch))
		if err := writeBundle(out, m, []file{{path: entry, data: data, exec: true}}); err != nil {
			return err
		}
	}

	// Universal bundle.
	var files []file
	overrides := map[string]any{}
	var platforms []string
	defaultCmd := ""
	add := func(goos string, data []byte) {
		entry := "server/" + osToPlatform[goos] + "/" + exe(name, goos)
		files = append(files, file{path: entry, data: data, exec: true})
		platforms = append(platforms, osToPlatform[goos])
		if defaultCmd == "" {
			defaultCmd = "${__dirname}/" + entry
		} else {
			overrides[osToPlatform[goos]] = map[string]any{"command": "${__dirname}/" + entry}
		}
	}
	switch amd, arm := bins["darwin_amd64"], bins["darwin_arm64"]; {
	case amd != nil && arm != nil:
		fat, err := machoFat(amd, arm)
		if err != nil {
			return fmt.Errorf("universal macOS binary: %w", err)
		}
		add("darwin", fat)
	case arm != nil:
		add("darwin", arm)
	case amd != nil:
		add("darwin", amd)
	}
	if b := firstOf(bins, "windows_amd64", "windows_arm64"); b != nil {
		add("windows", b)
	}
	if b := firstOf(bins, "linux_amd64", "linux_arm64"); b != nil {
		add("linux", b)
	}
	out := filepath.Join(outDir, fmt.Sprintf("%s_%s.mcpb", name, version))
	return writeBundle(out, manifest(version, defaultCmd, platforms, overrides), files)
}

func manifest(version, command string, platforms []string, overrides map[string]any) map[string]any {
	type tool struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	var tools []tool
	if cat, err := catalog.Get(); err == nil {
		for _, t := range cat.Tools {
			d := t.Title
			if d == "" {
				d = t.Method + " " + t.Path
			}
			tools = append(tools, tool{t.Name, d})
		}
	}
	mcpConfig := map[string]any{
		"command": command,
		"args":    []string{"stdio"},
		"env": map[string]string{
			"HCLOUD_API_SERVER": "${user_config.api_server}",
			"HCLOUD_API_TOKEN":  "${user_config.api_token}",
		},
	}
	if len(overrides) > 0 {
		mcpConfig["platform_overrides"] = overrides
	}
	return map[string]any{
		"manifest_version": "0.3",
		"name":             name,
		"display_name":     "helmut.cloud",
		"version":          version,
		"description":      "Access the helmut.cloud API from Claude.",
		"long_description": "Exposes the helmut.cloud microservice REST APIs as MCP tools, generated from their OpenAPI specifications.",
		"author":           map[string]string{"name": "MoovIT SP GmbH"},
		"server": map[string]any{
			"type":        "binary",
			"entry_point": command[len("${__dirname}/"):],
			"mcp_config":  mcpConfig,
		},
		"tools":           tools,
		"tools_generated": false,
		"compatibility":   map[string]any{"platforms": platforms},
		"user_config": map[string]any{
			"api_server": map[string]any{
				"type":        "string",
				"title":       "API server",
				"description": "Base URL of the helmut.cloud API, e.g. https://eu-central-1.app.helmut.cloud",
				"required":    true,
			},
			"api_token": map[string]any{
				"type":        "string",
				"title":       "API token",
				"description": "Token used to authenticate against the helmut.cloud API",
				"sensitive":   true,
				"required":    true,
			},
		},
	}
}

func writeBundle(out string, manifest map[string]any, files []file) error {
	mj, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	all := append([]file{{path: "manifest.json", data: mj}}, files...)
	for _, f := range all {
		h := &zip.FileHeader{Name: f.path, Method: zip.Deflate, Modified: time.Now()}
		h.SetMode(0o644)
		if f.exec {
			h.SetMode(0o755)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		if _, err := io.Copy(w, bytes.NewReader(f.data)); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%.1f MiB)\n", out, float64(buf.Len())/(1<<20))
	return nil
}

func binName(goos, arch string) string { return exe(fmt.Sprintf("%s_%s_%s", name, goos, arch), goos) }

func exe(base, goos string) string {
	if goos == "windows" {
		return base + ".exe"
	}
	return base
}

func firstOf(m map[string][]byte, keys ...string) []byte {
	for _, k := range keys {
		if b := m[k]; b != nil {
			return b
		}
	}
	return nil
}
