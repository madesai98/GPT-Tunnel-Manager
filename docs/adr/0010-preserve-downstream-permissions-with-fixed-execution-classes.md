# Preserve downstream permissions with fixed execution classes

Status: accepted

The Manager MCP uses a fixed set of ten execution tools derived only from normalized downstream MCP `ToolAnnotations`, rather than one generic executor or one upstream tool per downstream tool. `get_tool` mints an authenticated generation-bound Execution Handle for the expected class, and execution rejects class mismatches. This keeps the upstream tool count constant while preserving the host-visible read-only, destructive, idempotent, and open-world permission semantics as closely as a routed tool surface permits.