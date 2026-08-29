# GPT Tunnel Manager v2.0.0

GPT Tunnel Manager v2 is a major architecture rewrite. The application now exposes one stable Manager MCP and directly connects to downstream MCP servers instead of creating a tunnel/plugin/lifecycle stack for every server.

## Highlights

- One fixed 19-tool Manager MCP for indexing, discovery, tool detail, Routing Preferences, and permission-preserving execution.
- Direct downstream MCP connectivity over Stdio, Managed HTTP, and External HTTP.
- Four lifecycle modes: Always On, Managed, Manual, and Disabled.
- Router-native Managed Use Leases with automatic start, active-call/task protection, and idle-stop.
- Generation-based semantic Tool Catalog with atomic promotion, deterministic routing-state freshness, and crash-safe staging.
- Agent-driven semantic enrichment, capability reconciliation, non-blocking Ambiguity Reviews, and Routing Profiles/Preferences.
- Generation-bound authenticated Execution Handles and ten MCP ToolAnnotation-derived execution classes.
- Live downstream tool-contract drift detection that fails closed before unsafe dispatch.
- Modern stateless and legacy stateful upstream MCP compatibility, including required task/resource/callback continuation behavior.
- Downstream OAuth and static credential support with credentials kept outside normal configuration/index data.
- Local Manager capability protection enabled by default plus unconditional browser-Origin rejection.
- One optional Manager Secure MCP Tunnel. `tunnel-client` is retained only for this Manager Tunnel.
- Native Gio desktop management for servers, authentication, indexing, reviews, routing preferences, settings, logs, tray behavior, tunnel-client management, and application self-update.

## Important v1 upgrade note

V2 is intentionally a clean product/configuration break. Existing v1 configuration and routing data are not parsed or converted into v2 state. The updater preserves the Portable Root user-data directories while the v2 first-launch/cutover logic treats legacy v1 configuration/routing state as opaque legacy data and initializes strict v2-native state.

The obsolete packaged `lifecycle-skill/` directory is removed during v2 replacement and is rejected if it appears in a v2 release archive.

## Included builds

- Windows x64
- Windows ARM64
- Linux x64
- Linux ARM64
- macOS Intel x64
- macOS Apple Silicon ARM64
- Source archives
- `SHA256SUMS.txt`

## Verification

The v2 release gate includes committed module-graph verification, repository-wide tests, repository-wide race tests in CI, the dedicated search-quality gate, self-update clean-break tests, `go vet`, retrieval-scale benchmarks, six native desktop release builds, six `CGO_ENABLED=0` headless cross-builds, and Windows process/DPAPI checks.

See `README.md`, `CONTEXT.md`, `docs/V2_IMPLEMENTATION_PLAN.md`, and `docs/IMPLEMENTATION_STATUS.md` for the complete v2 architecture and implementation history.
