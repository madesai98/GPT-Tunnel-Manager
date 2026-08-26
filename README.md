# GPT Tunnel Manager

GPT Tunnel Manager is a portable Go application for running and supervising multiple local MCP servers through separate OpenAI Secure MCP Tunnels, plus a dedicated Manager MCP tunnel for lifecycle control from ChatGPT.

## Current architecture

- No authentication layer is added by GPT Tunnel Manager. Each MCP server is responsible for its own local/upstream authentication when needed.
- The OpenAI runtime API key is used only by `tunnel-client` to access the OpenAI tunnel control plane.
- Every Server Entry has its own immutable `srv_...` ID and its own OpenAI `tunnel_...` ID.
- Server modes: Always On, Managed, Manual.
- Transports: stdio, manager-owned HTTP, external HTTP.
- The Manager MCP exposes exactly four tools: `get_status`, `start`, `restart`, `shutdown`.
- Manager lifecycle mutations accept configured Server IDs only; they cannot inject commands, paths, environment variables, secrets, or tunnel IDs.
- `tunnel-client` runs in the foreground and is owned as part of the server runtime process group/tree.
- Managed idle shutdown is driven by structured `tunnel-client` dispatcher telemetry and is disabled when telemetry compatibility is not known.
- Configuration lives beside the executable in a strict Portable Root.

## Run

```bash
go run ./cmd/tunnel-manager
```

On first start the app creates `config/manager.json` and `config/servers.json` under the Portable Root and opens the loopback management UI in your browser. The UI can configure the Manager tunnel, store runtime API keys in the platform secret store, add/edit Server Entries, control lifecycle, view/export logs, and install or roll back `tunnel-client`.

Useful CLI operations:

```bash
tunnel-manager version
tunnel-manager print-root
tunnel-manager init
tunnel-manager validate
tunnel-manager marker srv_0123456789abcdef0123456789abcdef
printf '%s' "$CONTROL_PLANE_API_KEY" | tunnel-manager secret put secret://openai/runtime/default
```

## First-time setup

1. Create a Manager tunnel in OpenAI Platform and one tunnel for each MCP server you want to expose.
2. Create a Restricted Runtime API key with Tunnels Read + Use.
3. Open GPT Tunnel Manager settings and store the key under a `secret://...` reference.
4. Configure the Manager tunnel ID and credential reference.
5. Add Server Entries. Use one Developer Plugin per Server Entry in ChatGPT Developer Mode.
6. Put this marker in every participating Developer Plugin description:

```text
Managed by GPT Tunnel Manager.
GTM_SERVER_ID=<server-id>
Follow the GPT Tunnel Manager Lifecycle Skill before using this plugin.
```

7. Add `assets/lifecycle-skill/SKILL.md` separately as the lifecycle skill. The Manager Developer Plugin itself only connects to the Manager tunnel and exposes the four Manager MCP tools.

## Secret storage

- Windows: DPAPI (`CurrentUser`) with only encrypted ciphertext stored below Portable Root.
- macOS: Keychain through the system `security` utility.
- Linux: Secret Service through `secret-tool`; if Secret Service is unavailable or locked, secret operations fail rather than falling back to plaintext.
- Environment override is available through a deterministic `GTM_SECRET_<hash>` variable for controlled deployments.

Configuration files never contain secret values.

## tunnel-client

Unless `tunnel_client.binary_path` is configured, GPT Tunnel Manager downloads the latest official `openai/tunnel-client` release for the current OS/architecture, verifies the release SHA-256 digest, extracts it under `tools/tunnel-client/<version>/`, and atomically selects it for future starts. Existing foreground runtimes keep their current binary until restarted.

## Development

```bash
go test ./...
go vet ./...
go build ./cmd/tunnel-manager
```

CI also cross-compiles the command for Windows, Linux, and macOS on amd64 and arm64.
