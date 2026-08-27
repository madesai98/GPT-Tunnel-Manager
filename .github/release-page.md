# GPT Tunnel Manager v1.0.22

## Manager MCP connector compatibility

This release updates the built-in GPT Tunnel Manager MCP for current ChatGPT Developer Mode connector discovery.

- Added stateless MCP `2026-07-28` `server/discover` support.
- Preserved compatibility with legacy MCP `2025-06-18` `initialize` clients.
- Removed the misleading `Mcp-Session-Id` response header so the Manager MCP no longer advertises session/SSE semantics it does not implement.
- Made discovery and tool-list responses stateless and cache-aware for hosted connector discovery.
- Added regression coverage for modern discovery, legacy initialization, stateless requests, and GET/SSE behavior.
- Keeps the Manager lifecycle tool surface unchanged: `get_status`, `start`, `restart`, and `shutdown`.

This work targets the failure mode where the Manager tunnel is healthy and polling the OpenAI control plane, but ChatGPT still fails while creating the Developer Mode connector.

## Compatibility

- Existing GPT Tunnel Manager user data and downstream server configuration are unchanged.
- No additional Manager/plugin authentication layer is introduced.
- Existing downstream MCP tunnel behavior is unchanged by this release.

## Included builds

- Windows x64
- Windows ARM64
- Linux x64
- Linux ARM64
- macOS Intel x64
- macOS Apple Silicon ARM64
- Source archives
- `SHA256SUMS.txt`

## Changes since v1.0.21

- Support stateless MCP discovery for Manager.
- Add Manager MCP discovery compatibility tests.
- Merge PR #13: Modernize Manager MCP connector discovery.
