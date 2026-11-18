#!/bin/sh
# Regenerates the embedded tool catalog from the live OpenAPI specs.
# Used by goreleaser's before hook. The catalog is gitignored, so this never
# dirties the git tree.
#
# - HCLOUD_API_SERVER set:   fetch specs and regenerate
# - not set, catalog exists: reuse it (local snapshots)
# - neither:                 fail, rather than release a binary without tools
set -eu
catalog=internal/catalog/generated/catalog.json

if [ -n "${HCLOUD_API_SERVER:-}" ]; then
	exec go run ./cmd/gen -config services.yaml -out "$catalog"
elif [ -f "$catalog" ]; then
	echo "HCLOUD_API_SERVER not set: reusing existing $catalog" >&2
else
	echo "HCLOUD_API_SERVER not set and $catalog missing: cannot build without a tool catalog" >&2
	exit 1
fi
