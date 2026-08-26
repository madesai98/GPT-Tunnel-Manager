# GPT Tunnel Manager

GPT Tunnel Manager is a portable Go desktop application for supervising local MCP servers through separate OpenAI Secure MCP Tunnels, plus a dedicated Manager MCP tunnel that lets ChatGPT control eligible server lifecycles.

## Architecture

- GPT Tunnel Manager adds no authentication layer to the Manager MCP or participating server tunnels. Each MCP server remains responsible for any authentication it needs.
- The OpenAI Runtime API key is used only by `tunnel-client` to access the OpenAI tunnel control plane.
- Every Server Entry has an immutable `srv_...` ID and its own OpenAI `tunnel_...` ID.
- Server modes are Always On, Managed, and Manual.
- Transport types are Stdio, Managed HTTP, and External HTTP.
- The Manager MCP exposes exactly four tools: `get_status`, `start`, `restart`, and `shutdown`.
- Manager MCP lifecycle mutation accepts configured Server IDs only. It cannot receive executable paths, command arguments, environment variables, secret values, or tunnel IDs.
- `tunnel-client` runs in the foreground under Tunnel Manager process ownership. Managed HTTP child processes are also owned by Tunnel Manager; External HTTP targets are never terminated by it.
- Managed idle shutdown is enabled only when the installed `tunnel-client` telemetry format is explicitly known to support meaningful-activity classification.
- Mutable configuration and managed tooling live in the strict Portable Root beside the application.

## Desktop application

The normal executable is a native Gio desktop application with a notification-area/system-tray icon. There is no browser-based management UI.

The Servers page always begins with a built-in `Manager MCP` row. That row is not a normal Server Entry, cannot be deleted, and reports the Manager tunnel state with Manager-specific controls. Configured downstream Server Entries follow it.

The native UI provides:

- Manager MCP and downstream server status/lifecycle controls.
- Add/edit/delete controls for downstream Server Entries only.
- Stdio, Managed HTTP, and External HTTP configuration.
- Environment and custom secret-reference configuration.
- A dedicated masked `OpenAI Runtime API Key` field. The user enters only the key value; the fixed internal credential reference is not entered manually.
- Downstream tunnel configuration that uses the Manager Runtime API key by default.
- Custom-secret storage for downstream MCP/environment secrets.
- Tunnel-client update and rollback controls.
- Structured log filtering, clearing, and text/JSONL export.
- Launch-at-login, start-hidden-in-tray, close behavior, explicit exit confirmation, disk logging, and appearance settings.

Minimize and the configured close-to-tray behavior remove the native window from the taskbar while the Manager process, tray icon, tunnels, and owned servers continue running. `Open Manager` from the tray or a second-launch focus request restores a native Gio window. Explicit Exit stops owned MCP/tunnel processes and removes the tray icon.

Run from source:

```bash
go run ./cmd/tunnel-manager
```

Headless operation for diagnostics or controlled deployments:

```bash
go run -tags nogui ./cmd/tunnel-manager --no-gui
```

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

1. Create one Manager tunnel in OpenAI Platform and one tunnel for each MCP server you want to expose.
2. Create a Restricted Runtime API key with Tunnels Read + Use.
3. Start GPT Tunnel Manager. In Settings, paste only the Runtime API key value into `OpenAI Runtime API Key` and choose `Store API Key`.
4. Configure the Manager Tunnel ID. The Manager credential reference is fixed internally as `secret://openai/runtime/default`; the UI does not ask you to type it.
5. Add Server Entries. Their tunnel runtimes use the Manager Runtime API key by default. Create one ChatGPT Developer Mode plugin per Server Entry.
6. Put this marker in every participating Developer Plugin description:

```text
Managed by GPT Tunnel Manager.
GTM_SERVER_ID=<server-id>
Follow the GPT Tunnel Manager Lifecycle Skill before using this plugin.
```

7. Install `assets/lifecycle-skill/SKILL.md` separately as the generic lifecycle skill. The Manager Developer Plugin itself only connects to the Manager tunnel and exposes the four Manager MCP tools.

## Secret storage

- Windows: native DPAPI scoped to the current user; only ciphertext is stored under Portable Root.
- macOS: Keychain via the system `security` utility.
- Linux: Secret Service via `secret-tool`; unavailable or locked keyrings fail closed rather than storing plaintext.
- Controlled deployments may use the deterministic `GTM_SECRET_<hash>` environment override.

Configuration files contain secret references, never secret values. Secrets loaded at runtime are registered with the central redactor before related child output is retained.

The known OpenAI Runtime API key uses the fixed internal reference `secret://openai/runtime/default`. Arbitrary `secret://...` references are intended for custom downstream secrets rather than for normal Manager-key setup.

## tunnel-client

Unless `tunnel_client.binary_path` is configured, GPT Tunnel Manager:

1. Queries the official `openai/tunnel-client` latest release.
2. Selects the exact OS/architecture archive.
3. Requires and verifies the release asset SHA-256 digest.
4. Extracts into `tools/tunnel-client/<version>/`.
5. Runs a compatibility probe before promotion.
6. Atomically updates `active.json` while retaining a previous version for rollback.

Existing foreground runtimes keep the binary they started with until they are restarted.

## Development and verification

The repository uses Go 1.24 because the native Gio dependency requires it.

```bash
go mod tidy
git diff --exit-code -- go.mod go.sum
go test ./...
go vet ./...
go build ./cmd/tunnel-manager
```

CI verifies the native desktop build on Linux, Windows, and macOS and headless-compatible builds for:

- windows/amd64
- windows/arm64
- linux/amd64
- linux/arm64
- darwin/amd64
- darwin/arm64

Windows CI also exercises the native DPAPI secret-store round trip. Release Windows GUI binaries are linked with the Windows GUI subsystem so the normal desktop launch does not open a console window.

See `docs/IMPLEMENTATION_PLAN.md`, `docs/IMPLEMENTATION_STATUS.md`, and ADR 0008 for the current architecture.
