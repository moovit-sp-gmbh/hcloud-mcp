# Runtime image for `hcloud-mcp http` (streamable HTTP MCP server).
# The binary is built by goreleaser (dockers_v2) and placed in the build
# context at <os>/<arch>/hcloud-mcp, selected via TARGETPLATFORM.
#
# distroless/static: CA certificates + non-root user, no shell or package
# manager. The binary is static (CGO_ENABLED=0), so nothing else is needed.
FROM gcr.io/distroless/static-debian13:nonroot

LABEL org.opencontainers.image.source="https://github.com/moovit-sp-gmbh/hcloud-mcp"
LABEL org.opencontainers.image.description="An MCP for the helmut.cloud platform"
LABEL org.opencontainers.image.title="hcloud-mcp"

ARG TARGETPLATFORM
COPY --chmod=0755 ${TARGETPLATFORM}/hcloud-mcp /usr/local/bin/hcloud-mcp

# HCLOUD_API_SERVER  required: base URL of the helmut.cloud API
# HCLOUD_API_TOKEN   required in token mode "env" (pass at runtime, never bake in)
# apiTokenMode       "env" (default) or "external" (each client sends its own token)
# ADDR               listen address, also used by the healthcheck
ENV apiTokenMode="env" \
    ADDR=":8080" \
    LOG_LEVEL="info"

USER nonroot:nonroot
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/usr/local/bin/hcloud-mcp", "healthcheck"]

ENTRYPOINT ["/usr/local/bin/hcloud-mcp"]
CMD ["http"]
