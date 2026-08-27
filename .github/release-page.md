# GPT Tunnel Manager v1.0.23

## Official MCP Go SDK migration

This release replaces the built-in Manager MCP's handwritten MCP wire implementation with the official `modelcontextprotocol/go-sdk` v1.7.0.

- Uses the official SDK for MCP protocol discovery, negotiation, JSON-RPC handling, schemas, and Streamable HTTP behavior.
- Serves the Manager MCP in stateless mode for MCP `2026-07-28`, while the SDK handles compatible protocol negotiation instead of maintaining hard-coded wire behavior in GPT Tunnel Manager.
- Keeps the Manager lifecycle surface exactly four tools: `get_status`, `start`, `restart`, and `shutdown`.
- Uses typed Go inputs and outputs so the SDK generates and validates tool input/output schemas.
- Adds human-readable tool titles and explicit safety annotations for read-only versus lifecycle-changing operations.
- Preserves browser-origin rejection, lifecycle fail-closed behavior, and the existing no-auth Manager/plugin design.
- Moves project CI and release builds to Go 1.25, required by the current official SDK.
- Adds SDK-client integration tests and verifies native/headless builds across all supported platforms.

## Why this matters

Future MCP wire-level compatibility is now primarily owned by the official SDK. Updating to future MCP revisions should normally be a dependency upgrade plus migration testing rather than another handwritten protocol implementation.

## Compatibility

- Existing GPT Tunnel Manager user data and downstream server configuration are unchanged.
- Existing Manager tool names are unchanged.
- Existing downstream MCP tunnel behavior is unchanged.
- No additional Manager/plugin authentication layer is introduced.

## Included builds

- Windows x64
- Windows ARM64
- Linux x64
- Linux ARM64
- macOS Intel x64
- macOS Apple Silicon ARM64
- Source archives
- `SHA256SUMS.txt`

## Changes since v1.0.22

- Migrate the Manager MCP to the official `modelcontextprotocol/go-sdk` v1.7.0.
- Replace handwritten MCP discovery, transport, and tool wire handling with SDK-managed behavior.
- Add generated input/output schemas and explicit tool annotations.
- Update the project and release toolchain to Go 1.25.
- Add official-SDK integration tests and cross-platform build verification.
- Merge PR #14: Migrate Manager MCP to official Go SDK.
