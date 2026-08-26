# GPT Tunnel Manager v1 Implementation Contract

Status: implemented architecture. This document reflects the final no-auth design and supersedes Shared OAuth/Auth Gateway sections from earlier planning. ADR 0008 records that decision explicitly.

## 1. Product scope

GPT Tunnel Manager is a portable Go desktop application that manages local MCP Server Entries, one OpenAI Secure MCP Tunnel per Server Entry, a separate Manager MCP tunnel, lifecycle policy, `tunnel-client` installation/update, structured diagnostics, a native Gio control surface, and a generic ChatGPT Lifecycle Skill.

Supported desktop targets:

- Windows amd64 / arm64.
- Linux amd64 / arm64.
- macOS amd64 / arm64.

Supported Server Modes:

- Always On.
- Managed.
- Manual.

Supported transports:

- Stdio.
- Managed HTTP.
- External HTTP.

V1 deliberately does not:

- Merge multiple MCP servers into one Developer Plugin.
- Add an OAuth/Auth Gateway in front of the Manager MCP or individual server tunnels.
- Reimplement the OpenAI tunnel protocol.
- Install language runtimes such as Node.js, Python, Java, or Bun.
- Let ChatGPT lifecycle calls supply arbitrary commands, paths, environment values, secrets, or Tunnel IDs.
- Treat the diagnostics console as a shell.

## 2. Authentication and credential boundary

Tunnel Manager adds no authentication layer to MCP resources. Each MCP server handles its own authentication if required.

The OpenAI Runtime API key is separate from end-user/server authentication. It is resolved from a `secret://` reference at runtime and passed only to `tunnel-client` as `CONTROL_PLANE_API_KEY` so the tunnel runtime can connect to OpenAI's control plane.

The Manager Developer Plugin connects directly through the Manager tunnel to the loopback Manager MCP. Each participating Developer Plugin connects through its own tunnel directly to that entry's configured Stdio or HTTP target.

The advanced loopback web UI uses a random per-process SameSite/HttpOnly session cookie and same-origin checks for state-changing routes. This is a localhost CSRF boundary, not an MCP authentication mechanism.

## 3. Repository structure

```text
cmd/tunnel-manager/       native/headless application entry point
internal/app/             composition, startup, shutdown, updater
internal/admin/           loopback advanced web UI
internal/config/          strict schema-v1 config and atomic persistence
internal/events/          typed runtime event bus
internal/instance/        single Portable Root ownership and focus IPC
internal/lifecycle/       desired/observed states and backoff
internal/lifecycleskill/  compiled generic skill text for packaged export
internal/logging/         redaction, bounded ring, optional rotating disk sink
internal/marker/          lifecycle marker generation
internal/mcpmanager/      four-tool Manager MCP
internal/platform/        URL/folder and launch-at-login integration
internal/portable/        strict Portable Root resolution
internal/process/         child process/process-tree ownership
internal/productlinks/    OpenAI/ChatGPT destinations
internal/secrets/         platform secret-store abstraction
internal/servers/         registry, supervisors, transport runtimes
internal/tunnelclient/    official release install/update and runtime adapter
assets/lifecycle-skill/   separately installable ChatGPT skill
```

## 4. Configuration

All ordinary mutable files live below Portable Root. `config/manager.json` and `config/servers.json` use schema version 1 and are decoded with unknown-field rejection.

Manager settings include:

- Manager Tunnel ID and runtime credential reference.
- Launch at login, start minimized, tray behavior, close behavior, exit confirmation.
- Default Managed idle timeout.
- Logging capture/display settings, memory budget, optional disk rotation.
- `tunnel-client` binary override, update policy, and check interval.
- Appearance theme.

Every Server Entry includes:

- Immutable `srv_<32 lowercase hex>` ID.
- Display name and optional Developer Plugin name.
- Enabled state and Server Mode.
- One transport definition.
- One `tunnel_<32 lowercase hex>` ID.
- Optional per-entry runtime credential reference.
- Plain environment values and secret environment references.
- Startup/shutdown/idle timeouts.
- Optional logging override field for forward-compatible UI configuration.

Commands are executable + argument array. There is no persisted shell command.

Writes validate the complete prospective object, write a sibling temporary file, flush it, and atomically replace the target.

## 5. Secret storage and redaction

Configuration stores secret references such as:

```text
secret://openai/runtime/default
secret://server/srv_.../env/MY_TOKEN
```

Platform implementations:

- Windows: DPAPI scoped to CurrentUser, ciphertext only below Portable Root.
- macOS: Keychain via the system `security` utility.
- Linux: Secret Service via `secret-tool`; unavailable or locked stores fail closed.
- Controlled environments may provide deterministic `GTM_SECRET_<hash>` overrides.

Every runtime secret that is loaded or written is registered with the central redactor before related process output is retained. Authorization/token/secret-like structured fields are redacted independently of known values.

## 6. Process and transport ownership

Each active Server Entry owns an independent runtime boundary.

Stdio:

- `tunnel-client` receives the configured MCP command and owns the Stdio server descendant.
- No shell is invoked by Tunnel Manager.

Managed HTTP:

- Tunnel Manager launches the configured HTTP MCP process directly from executable + argv.
- `tunnel-client` connects to its configured MCP URL.
- Both processes are stopped when the entry stops.

External HTTP:

- Tunnel Manager launches only `tunnel-client`.
- The configured external/local HTTP server is never terminated by Tunnel Manager.

Unix-like systems launch Manager-owned child trees in process groups and terminate the group gracefully then forcibly when necessary. Windows creates a new process group and terminates descendants as a process tree. Application shutdown stops all entry runtimes plus the Manager tunnel.

## 7. Lifecycle model

Observed states:

- `stopped`
- `starting`
- `ready`
- `degraded`
- `retry_wait`
- `stopping`

Always On:

- Enabled entries are started when Tunnel Manager starts.
- Unexpected failure preserves Desired Running and uses bounded retry/backoff.
- Manager MCP cannot mutate the entry.

Managed:

- Starts stopped after a full application restart.
- UI and Manager MCP may start/restart/shutdown it.
- Unexpected failure while Desired Running retries with bounded backoff.
- Meaningful activity resets the idle timer when telemetry compatibility is known.

Manual:

- Starts stopped after a full application restart.
- Only the desktop/local admin UI may mutate lifecycle.
- Manager MCP may report status but cannot start/restart/shutdown it.

Disabled:

- Cannot start until re-enabled.

Lifecycle operations are serialized per entry. Duplicate start of an already active entry and duplicate stop of an already stopped entry are idempotent and do not create duplicate runtimes.

## 8. Readiness and Managed Activity

Ready requires the launched `tunnel-client` to write its dynamic health URL and return HTTP 200 from `/readyz`. Owned Managed HTTP child startup must also succeed.

Managed idle activity is inferred only from structured `tunnel-client` JSON telemetry for versions explicitly classified as compatible. Initialization, notifications, ping, health traffic, and other routine chatter do not reset idle timeout. Unknown telemetry versions disable idle shutdown rather than guessing.

## 9. Manager MCP contract

The Manager MCP binds to loopback and exposes exactly four tools.

`get_status`

```json
{
  "server_id": "srv_...",
  "wait_for_ready": false,
  "timeout_seconds": 30
}
```

`server_id` may be omitted to list all entries. Waiting requires one Server ID and is bounded to 60 seconds.

`start`, `restart`, and `shutdown`

```json
{"server_id":"srv_..."}
```

Mutation is allowed only for configured Managed entries. Disabled entries return `server_disabled`; Always On/Manual mutation returns `mode_not_mcp_controllable`.

The JSON schema rejects additional properties, and the server performs strict decoding as a second enforcement layer. Browser requests carrying an `Origin` header are rejected; the endpoint is for owned `tunnel-client` traffic rather than arbitrary localhost web access.

## 10. Lifecycle Marker and Skill

Participating per-server Developer Plugins contain:

```text
Managed by GPT Tunnel Manager.
GTM_SERVER_ID=<server-id>
Follow the GPT Tunnel Manager Lifecycle Skill before using this plugin.
```

The Manager Developer Plugin does not carry the marker.

The generic Lifecycle Skill:

1. Parses the immutable Server ID.
2. Calls Manager `get_status`.
3. Fails closed when the Manager plugin is unreachable or the entry is disabled.
4. Never mutates Always On or Manual entries.
5. Starts/waits/restarts Managed entries according to observed state.
6. Invokes the target plugin only after Ready.
7. May shut down a Managed entry it started when work is clearly complete; idle timeout is the fallback cleanup.

The skill is available as `assets/lifecycle-skill/SKILL.md` and is also compiled into the executable so export works from packaged builds without a source checkout.

## 11. tunnel-client installation and updates

When no binary override is configured:

1. Query the official `openai/tunnel-client` latest release metadata.
2. Select the exact current OS/architecture archive.
3. Require a release-provided SHA-256 digest.
4. Download into Portable Root `data/` with a size bound.
5. Verify the downloaded archive digest before extraction.
6. Extract only the expected `tunnel-client[.exe]` payload.
7. Run a bounded compatibility probe (`help quickstart`).
8. Atomically promote `active.json`.
9. Remember the previous known version for rollback.
10. Existing runtimes continue using their already-running binary until restarted.

The updater performs an early startup check and then follows the configured interval. Automatic promotion is optional.

## 12. Logging

Retained events contain timestamp, level, source, component, message, and sanitized structured fields.

The memory ring is bounded by the configured 5/10/25/50/100 MB budget and evicts oldest events first.

Disk logging is disabled by default. When enabled, JSONL logs rotate by configured file size and retention count. Logging settings can be reconfigured at runtime. Native and advanced UI surfaces support clearing and text/JSONL export.

Raw MCP payload logging is not enabled. Stdio protocol stdout remains owned by `tunnel-client` rather than being treated as ordinary Tunnel Manager console output.

## 13. Native desktop and tray

The normal application opens a Gio desktop window with pages for:

- Server list/status/lifecycle.
- Server editor and Lifecycle Marker display.
- Logs/filter/export.
- Manager settings, secret entry, update/rollback, startup/tray/close behavior, and appearance.

Normal title-bar close is app-controlled:

- `minimize` keeps Tunnel Manager running and minimizes the native window.
- `exit` follows the explicit-exit path.

Explicit exit from the window or tray optionally asks for confirmation and explains that all tunnels and owned MCP servers will stop. The user can disable future confirmation.

When tray integration is enabled, the menu includes Open Manager, status summary, Advanced Web UI, and Exit Tunnel Manager. Second-instance startup requests focus from the existing owner rather than launching a competing supervisor.

## 14. Advanced loopback UI

A secondary advanced web surface binds to a random loopback port. Read-only state/log routes remain same-machine endpoints. State-changing routes require a random per-process HttpOnly/SameSite session cookie set by the UI root and reject foreign `Origin` values.

This surface is never tunneled as the Manager MCP and is not a remote administration API.

## 15. Startup sequence

1. Resolve executable and Portable Root.
2. Verify Portable Root is writable.
3. Acquire single-owner instance endpoint for that Portable Root.
4. Load/validate configuration.
5. Initialize secrets, redaction, logging, event bus, installer, registry.
6. Start loopback Manager MCP.
7. Start loopback advanced admin UI.
8. Start/retry the Manager tunnel when configured.
9. Start enabled Always On entries with bounded concurrency.
10. Start native Gio UI and tray according to settings.
11. Run updater and Managed idle timers.

Launch-at-login starts the normal desktop application, not a hidden headless substitute. `StartMinimized` controls initial window state.

## 16. Shutdown sequence

1. Stop accepting new Manager MCP mutations.
2. Cancel application runtime/updater timers.
3. Stop Server Entries with bounded concurrency.
4. Stop Manager tunnel runtime.
5. Stop advanced admin UI.
6. Flush/close disk logging.
7. Release single-instance ownership after application termination.

External HTTP MCP services are not terminated.

## 17. Security boundaries

- Manager MCP tools accept only Server IDs.
- Manager MCP rejects browser-originated requests.
- Advanced-web mutations require a same-site local session token.
- Manager and admin endpoints bind only to loopback.
- Child commands are executed directly, never through a shell.
- Runtime release downloads require SHA-256 verification before use.
- Secret values are never written to JSON configuration and are redacted before retained logging.
- The diagnostics UI is not an interactive shell.

## 18. Verification and CI

Required gates:

- `go mod tidy` produces no diff to committed `go.mod` / `go.sum`.
- `go test ./...`.
- `go vet ./...`.
- Native GUI compilation on Linux, Windows, and macOS.
- Headless-compatible builds for Windows/Linux/macOS on amd64 and arm64.

Security regression tests cover the four-tool contract, additional-property rejection, browser-Origin rejection for Manager MCP, local-admin mutation session enforcement, redaction, and fail-closed release digest handling.

Real OpenAI tunnel acceptance remains an operator/release-gated test because CI does not contain production Runtime API keys or permanent tunnel resources.
