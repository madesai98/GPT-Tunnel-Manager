# Implementation Status

Date: 2026-08-26

Status: v1.0.0 is the released baseline. The current v1.0.1 work corrects native desktop UX and Windows integration while preserving the existing runtime architecture. The v1.0.1 release is not considered complete until its branch CI, post-merge CI, and release assets are verified.

ADR 0008 is authoritative for authentication architecture and supersedes ADR 0007 plus all Shared OAuth/Auth Gateway language from earlier planning.

## Current architecture

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

- Native Gio is the only management UI; there is no management Web UI.
- System-tray-first integration: minimize/close-to-tray removes the native window from the taskbar while the process remains active.
- Start-minimized starts directly in the tray without requiring a visible management window.
- Tray/second-instance Open Manager restores a native Gio window.
- The Servers page begins with a built-in, non-deletable `Manager MCP` row that is not persisted in `servers.json`.
- Server list/status/lifecycle controls for downstream entries.
- Server editor for all three transports, environment values, and custom secret references.
- Dedicated masked value-only `OpenAI Runtime API Key` field backed by the fixed internal reference `secret://openai/runtime/default`.
- Downstream tunnel runtimes use the Manager Runtime API key by default.
- Windows secret storage uses native Current User DPAPI rather than PowerShell cryptography access.
- Tunnel-client check/install/rollback controls.
- Structured live logs with search, level filter, clear, text export, and JSONL export.
- Launch-at-login, start-hidden-in-tray, close behavior, exit-confirmation, logging, and appearance settings.
- App-controlled native title-bar close/minimize behavior and coordinated explicit exit confirmation.
- Runtime disk-log reconfiguration and bounded rotation.
- Windows release GUI binaries use the GUI subsystem so the desktop launch does not open a console window.

### Authentication architecture

- No Tunnel Manager OAuth/Auth Gateway.
- No additional Manager plugin authentication.
- No additional per-server tunnel authentication layer.
- Each MCP server owns its own authentication needs.
- OpenAI Runtime API keys are used solely by `tunnel-client` for Secure MCP Tunnel control-plane access.

## Verification gates for v1.0.1

The release remains gated on:

- `go mod tidy` producing no diff to committed `go.mod` / `go.sum`.
- `go test ./...`.
- `go vet ./...`.
- Native Gio desktop builds on Ubuntu, Windows, and macOS.
- Windows native DPAPI round-trip test.
- Headless-compatible builds for Windows/Linux/macOS on amd64 and arm64.
- Post-merge CI on `main`.
- Successful native release builds for Windows AMD64/ARM64, Linux AMD64/ARM64, and macOS AMD64/ARM64.
- Release changelog, source archives, and `SHA256SUMS.txt` verification.

## v1.0.0 baseline verification

The earlier v1.0.0 implementation passed the repository CI matrix before the native-only UX corrections. Those historical results remain baseline evidence only and do not substitute for v1.0.1 validation.

## Deliberate external acceptance boundary

Automated CI does not create or consume real OpenAI Secure MCP Tunnels because the repository contains no production Runtime API key or permanent tunnel resources. Real ChatGPT Developer Plugin discovery and end-to-end tunnel traffic are release/operator acceptance tests using suitable credentials and tunnels.
