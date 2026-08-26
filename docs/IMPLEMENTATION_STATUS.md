# Implementation Status

Date: 2026-08-26

Status: v1 implementation complete in code; merge is gated by the repository CI matrix described below.

ADR 0008 is authoritative for authentication architecture and supersedes ADR 0007 plus all Shared OAuth/Auth Gateway language from earlier planning.

## Implemented

### Runtime and persistence

- Strict Portable Root beside the executable/application bundle.
- Schema-v1 `manager.json` and `servers.json` with unknown-field rejection.
- Stable random immutable Server IDs.
- Atomic config writes.
- Platform secret storage and controlled environment overrides.
- Secret registration/redaction before retained logging.
- Single Portable Root ownership with focus handoff to an existing instance.

### Process and tunnel substrate

- Direct executable + argv process launching; no shell command execution.
- Unix process-group termination and Windows process-tree termination.
- Foreground official `tunnel-client` ownership.
- Dynamic health URL file and `/readyz` readiness.
- Stdio, Managed HTTP, and External HTTP transports.
- Exact OS/architecture release selection.
- Required SHA-256 release digest verification.
- Compatibility probe before version promotion.
- Atomic active-version selection and rollback to a previous installed version.

### Lifecycle

- Desired and Observed State separation.
- Always On, Managed, Manual, and Disabled policy enforcement.
- Serialized idempotent starts/restarts/shutdowns.
- Crash detection and bounded jittered retry/backoff.
- Stable-run backoff reset.
- Managed idle shutdown driven only by explicitly compatible structured tunnel-client telemetry.
- Always On startup and coordinated application shutdown.

### Manager MCP and ChatGPT lifecycle

- Loopback Manager MCP exposed through its own Secure MCP Tunnel.
- Exactly four tools: `get_status`, `start`, `restart`, `shutdown`.
- Managed-only lifecycle mutation gate.
- Server-ID-only mutation schema with strict additional-property rejection.
- Browser-Origin rejection for the localhost Manager MCP.
- Generic immutable Lifecycle Marker.
- Generic separately installed Lifecycle Skill.
- Compiled Lifecycle Skill content for packaged export.

### Desktop and local UX

- Native Gio desktop control surface.
- System tray integration.
- Server list/status/lifecycle controls.
- Server editor for all three transports, environment values, and secret references.
- Manager tunnel and runtime credential settings.
- Native secret entry.
- Tunnel-client check/install/rollback controls.
- Structured live logs with search, level filter, clear, text export, and JSONL export.
- Launch-at-login, start-minimized, tray, close behavior, exit-confirmation, logging, and appearance settings.
- App-controlled native title-bar close behavior.
- Coordinated explicit-exit confirmation from window and tray.
- Advanced loopback web UI retained as a secondary surface.
- Same-site per-process session protection for advanced-web mutation endpoints.
- Runtime disk-log reconfiguration and bounded rotation.

### Authentication architecture

- No Tunnel Manager OAuth/Auth Gateway.
- No additional Manager plugin authentication.
- No additional per-server tunnel authentication layer.
- Each MCP server owns its own authentication needs.
- OpenAI Runtime API keys are used solely by `tunnel-client` for Secure MCP Tunnel control-plane access.

## Verification gates

The final merge requires all of the following on the final commit:

- Committed `go.mod` / `go.sum` are tidy with zero generated diff.
- `go test ./...` succeeds.
- `go vet ./...` succeeds.
- Native desktop build succeeds on Ubuntu.
- Native desktop build succeeds on Windows.
- Native desktop build succeeds on macOS.
- Headless-compatible build succeeds for windows/amd64.
- Headless-compatible build succeeds for windows/arm64.
- Headless-compatible build succeeds for linux/amd64.
- Headless-compatible build succeeds for linux/arm64.
- Headless-compatible build succeeds for darwin/amd64.
- Headless-compatible build succeeds for darwin/arm64.

The prior native-shell commit already passed native builds on all three desktop OS runners, `go test`, `go vet`, and all six headless cross-builds. The final hardening/documentation commit is intentionally rerun through the same complete matrix before merge.

## Deliberate external acceptance boundary

Automated CI does not create or consume real OpenAI Secure MCP Tunnels because the repository contains no production Runtime API key or permanent tunnel resources. Real ChatGPT Developer Plugin discovery and end-to-end tunnel traffic are release/operator acceptance tests using disposable credentials and tunnels.
