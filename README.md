# GPT Tunnel Manager

GPT Tunnel Manager is a portable Go desktop application that exposes one stable Manager MCP and routes its fixed tool surface to configured downstream MCP servers.

V2 connects to downstream MCPs directly. Per-server Secure MCP Tunnels, per-server Developer Plugins, lifecycle markers, and lifecycle skills are not part of the v2 architecture.

## Architecture

- One fixed 19-tool Manager MCP is exposed at a loopback `/mcp` endpoint.
- The Manager surface covers indexing/enrichment, discovery/detail, Routing Preferences, and ten permission-preserving execution classes.
- Downstream MCPs connect directly over Stdio, Managed HTTP, or External HTTP.
- Server modes are Always On, Managed, Manual, and Disabled.
- Managed servers automatically start for routed work, remain alive while calls/tasks hold use leases, and idle-stop later.
- Tool discovery is backed by a generation-based semantic catalog rather than by dynamically copying every downstream tool into the upstream surface.
- Authoritative downstream schemas and MCP ToolAnnotations remain separate from semantic enrichment and Routing Preferences.
- Live downstream tool-contract drift fails closed and marks routing stale before another unsafe dispatch can occur.
- Optional local Manager capability protection is enabled by default. Browser-Origin requests are rejected regardless of that setting.
- One optional Manager Secure MCP Tunnel may expose the Manager MCP remotely. There are no per-server tunnels.
- `tunnel-client` is retained only for that optional Manager Tunnel.

## Native desktop application

The normal executable is a native Gio desktop application with system-tray support. There is no browser-based administration UI.

The desktop application provides:

- Server Entry add/edit/delete for all four lifecycle modes and all three downstream transports.
- Direct Start, Stop, and Restart controls backed by the same router-native lifecycle service used by MCP execution.
- Downstream OAuth Connect/Reconnect/Disconnect and static-header/API-key authentication.
- Environment and secret-environment configuration without requiring users to type internal secret-reference names.
- Embedding provider configuration, including base URL, model, optional dimensions, and credential storage.
- Index status, refresh, enrichment-batch visibility, Ambiguity Reviews, and atomic commit controls.
- Routing Profile and Routing Preference management with conflict/review state.
- Local Manager port and capability-protection controls.
- Optional Manager Secure MCP Tunnel configuration and runtime status.
- Structured log filtering, clearing, text/JSONL export, and secret redaction.
- `tunnel-client` install/update/rollback controls for the Manager Tunnel only.
- Launch-at-login, start-hidden-in-tray, close behavior, explicit exit confirmation, disk logging, and appearance settings.
- Application self-update using signed-by-release SHA-256 verification and a separate updater terminal/process.

Minimize and configured close-to-tray behavior remove the native window while the Manager process and eligible downstream runtimes continue running. Explicit Exit shuts down Manager-owned runtimes and removes the tray icon.

## First-time setup

1. Start GPT Tunnel Manager and configure the embedding provider in Settings.
2. Add downstream Server Entries. No Tunnel ID or ChatGPT Developer Plugin is required for a downstream server.
3. Use Index to refresh the catalog, complete required enrichment/capability reconciliation through the Manager MCP, and commit the ready generation.
4. Optionally create Routing Profiles and Routing Preferences.
5. Connect an MCP-capable client or agent to the Manager MCP.
6. Optionally create one OpenAI Secure MCP Tunnel for the Manager MCP and configure its Tunnel ID plus Runtime API key in Settings.

When Local Manager Access Protection is enabled, GPT Tunnel Manager supplies the local capability to its managed `tunnel-client` through an environment-backed MCP Authorization header. The capability is not placed in command-line arguments or configuration files.

## Manager MCP workflow

A normal agent workflow is:

```text
index_status / index_refresh
-> required enrichment + capability reconciliation
-> index_commit
-> search_tools
-> get_tool
-> one permission-class executor
```

`get_tool` returns the authoritative downstream tool contract separately from derived semantic guidance and provides the authenticated Execution Handle required by an executor.

The Manager also proxies protocol-required task/resource continuations and legacy callback behavior through Manager-owned mappings rather than replaying originating tool calls.

## Authentication boundaries

Manager Tunnel credentials, embedding credentials, downstream static credentials, downstream OAuth credentials/tokens, and local Manager capability protection are separate credential boundaries.

Credential values are stored through the platform secret store and are never persisted directly in `manager.json` or `servers.json`.

- Windows: DPAPI scoped to the current user.
- macOS: Keychain through the system `security` utility.
- Linux: Secret Service through `secret-tool`; unavailable/locked keyrings fail closed.
- Controlled deployments may use the deterministic `GTM_SECRET_<hash>` environment fallback.

Credential-bearing External/Managed HTTP endpoints require HTTPS unless the explicit insecure transport override is enabled for that Server Entry.

## Embeddings and routing data

Tool Catalog embeddings are derived routing artifacts. Search-query embeddings are memory-only and are not persistently cached by default.

If a remote embedding provider is configured, projected tool text/schema material and search queries used for embeddings are sent to that provider. Raw secret values are not part of the embedding/index projections.

Routing Preferences use their own preference revision and take effect without forcing semantic reindexing. A preference is marked for review rather than silently transferred if its referenced tool or routing assumptions change.

## Manager Secure MCP Tunnel

The optional Manager Tunnel uses the official `openai/tunnel-client` runtime.

Unless a binary override is configured, GPT Tunnel Manager can install/update `tunnel-client`, verify release checksums, probe compatibility, atomically promote the selected version, and retain a previous version for rollback.

The child process receives the OpenAI Runtime API key through its environment. When local Manager protection is enabled, its loopback Authorization header is also supplied through an environment reference rather than a secret-bearing argv value.

## Self-update

Application self-update downloads the selected release into a temporary directory, verifies its SHA-256 digest, stages only application files, launches an independent updater process/terminal, stops the running application, replaces application files, and restarts GPT Tunnel Manager.

The updater preserves the Portable Root user-data directories:

- `config/`
- `data/`
- `tools/`

The v2 clean break explicitly removes the obsolete packaged v1 `lifecycle-skill/` directory during replacement, and v2 release archives containing that obsolete path are rejected by the staging logic.

## Running from source

```bash
go run ./cmd/tunnel-manager
```

Headless operation:

```bash
go run -tags nogui ./cmd/tunnel-manager --no-gui
```

Useful CLI operations:

```bash
tunnel-manager version
tunnel-manager print-root
tunnel-manager init
tunnel-manager validate
printf '%s' "$VALUE" | tunnel-manager secret put secret://custom/example
```

The `secret` command is an advanced/automation surface. Ordinary native v2 setup does not require manually entering internal secret-reference names.

## Development and verification

The project CI uses Go 1.25.x.

```bash
go mod tidy
git diff --exit-code -- go.mod go.sum
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/tunnel-manager
```

CI verifies native desktop builds on all six release targets:

- windows/amd64
- windows/arm64
- linux/amd64
- linux/arm64
- darwin/amd64
- darwin/arm64

It also verifies all six `CGO_ENABLED=0` headless cross-build targets, Windows no-console child process behavior, Windows DPAPI storage, search-quality gates, retrieval-scale benchmarks, and the release/self-update clean-break tests.

See `CONTEXT.md`, `docs/V2_IMPLEMENTATION_PLAN.md`, `docs/IMPLEMENTATION_STATUS.md`, and ADR 0009 onward for the v2 architecture and implementation history.
