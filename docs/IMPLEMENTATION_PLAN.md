# GPT Tunnel Manager v1 Implementation Contract

Status: implemented architecture with v1.0.1 native-only UX corrections. ADR 0008 records the final no-auth decision and supersedes Shared OAuth/Auth Gateway language from earlier planning.

## 1. Product scope

GPT Tunnel Manager is a portable Go desktop application that manages local MCP Server Entries, one OpenAI Secure MCP Tunnel per Server Entry, a separate Manager MCP tunnel, lifecycle policy, `tunnel-client` installation/update, structured diagnostics, a native Gio control surface, a system tray, and a generic ChatGPT Lifecycle Skill.

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

- Provide a browser-based management UI.
- Merge multiple MCP servers into one Developer Plugin.
- Add an OAuth/Auth Gateway in front of the Manager MCP or individual server tunnels.
- Reimplement the OpenAI tunnel protocol.
- Install language runtimes such as Node.js, Python, Java, or Bun.
- Let ChatGPT lifecycle calls supply arbitrary commands, paths, environment values, secrets, or Tunnel IDs.
- Treat diagnostics as a shell.

A tiny loopback endpoint may be used for single-instance ownership/focus handoff. It is not a management interface.

## 2. Authentication and credential boundary

Tunnel Manager adds no authentication layer to MCP resources. Each MCP server handles its own authentication if required.

The OpenAI Runtime API key is separate from end-user/server authentication. It is passed only to `tunnel-client` as `CONTROL_PLANE_API_KEY` so tunnel runtimes can connect to OpenAI's control plane.

The Manager Runtime API key has one known internal reference:

```text
secret://openai/runtime/default
```

The native Settings page exposes a dedicated masked `OpenAI Runtime API Key` field and asks only for the key value. Users are not required to type the internal reference. Existing schema-v1 credential-reference data remains decodable for compatibility and is normalized/migrated to the canonical Manager reference when possible.

Downstream tunnel runtimes inherit the Manager Runtime API key by default. Arbitrary `secret://...` references are for genuinely custom downstream secrets/environment values rather than routine Manager-key setup.

The Manager Developer Plugin connects directly through the Manager tunnel to the loopback Manager MCP. Each participating Developer Plugin connects through its own tunnel directly to that entry's configured Stdio or HTTP target.

## 3. Repository structure

```text
cmd/tunnel-manager/       native/headless application entry point
internal/app/             composition, startup, shutdown, updater
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

There is no `internal/admin` Web UI component in the native-only architecture.

## 4. Configuration

All ordinary mutable files live below Portable Root. `config/manager.json` and `config/servers.json` use schema version 1 and are decoded with unknown-field rejection.

Manager settings include:

- Manager Tunnel ID.
- Canonical internal Manager Runtime credential reference.
- Launch at login, start hidden in tray, close behavior, and exit confirmation.
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
- A tunnel credential-reference field retained for schema compatibility/custom cases; blank means use the Manager Runtime API key.
- Plain environment values and secret environment references.
- Startup/shutdown/idle timeouts.
- Optional logging override field for forward-compatible UI configuration.

The normal native server editor does not expose a routine Runtime credential-reference field. New entries inherit the Manager key.

Commands are executable + argument array. There is no persisted shell command.

Writes validate the complete prospective object, write a sibling temporary file, flush it, and atomically replace the target.

## 5. Secret storage and redaction

Configuration stores secret references, never secret values.

Platform implementations:

- Windows: native DPAPI scoped to Current User, ciphertext only below Portable Root.
- macOS: Keychain via the system `security` utility.
- Linux: Secret Service via `secret-tool`; unavailable or locked stores fail closed.
- Controlled environments may provide deterministic `GTM_SECRET_<hash>` overrides.

Windows DPAPI uses native `CryptProtectData` / `CryptUnprotectData` and releases DPAPI-allocated output with `LocalFree`. The UI masks the Runtime API key and clears plaintext input after successful storage.

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
- Only the native desktop UI may mutate lifecycle.
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

## 10. Manager MCP row and downstream entries

The native Servers page renders the built-in `Manager MCP` row first.

- It is not stored in `servers.json`.
- It does not consume or expose an ordinary Server ID.
- It cannot be deleted or edited as a downstream server.
- It reports Manager tunnel configuration/readiness/degraded state.
- It exposes Manager-specific controls such as tunnel restart and Settings.

Configured downstream Server Entries follow the built-in row and retain their normal lifecycle/editor/delete behavior.

## 11. Lifecycle Marker and Skill

Participating per-server Developer Plugins contain:

```text
GTM PLUGIN | <server-id> | Follow the gpt-tunnel-manager-lifecycle skill before using this plugin
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

## 12. tunnel-client installation and updates

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

## 13. Logging

Retained events contain timestamp, level, source, component, message, and sanitized structured fields.

The memory ring is bounded by the configured 5/10/25/50/100 MB budget and evicts oldest events first.

Disk logging is disabled by default. When enabled, JSONL logs rotate by configured file size and retention count. Logging settings can be reconfigured at runtime. The native UI supports clearing and text/JSONL export.

Raw MCP payload logging is not enabled. Stdio protocol stdout remains owned by `tunnel-client` rather than being treated as ordinary Tunnel Manager console output.

## 14. Native desktop and tray

The normal application uses Gio as its only management UI and provides pages for:

- Manager MCP plus downstream server list/status/lifecycle.
- Server editor and Lifecycle Marker display.
- Logs/filter/export.
- Manager Tunnel ID, dedicated Runtime API key entry, custom secrets, update/rollback, startup/tray/close behavior, and appearance.

The system tray is part of normal native operation.

- Minimize hides the native window to the notification area instead of leaving a taskbar-minimized window.
- Close behavior `minimize` hides to the tray and keeps tunnels/processes running.
- Close behavior `exit` follows the explicit-exit path.
- Start minimized starts in the tray without requiring a visible Gio window.
- Tray `Open Manager` and second-instance focus handoff show/raise the native UI.
- Explicit Exit optionally asks for confirmation, stops owned MCP/tunnel processes, removes the tray icon, and exits.

A Gio `DestroyEvent` ends that native window instance. Hide-to-tray therefore does not attempt to reuse a destroyed `app.Window`; restoration uses a fresh native window instance while the Manager process and tray remain alive.

## 15. Startup sequence

1. Resolve executable and Portable Root.
2. Verify Portable Root is writable.
3. Acquire single-owner instance endpoint for that Portable Root.
4. Load/validate configuration and normalize the canonical Manager Runtime credential reference.
5. Initialize secrets, redaction, logging, event bus, installer, registry.
6. Start loopback Manager MCP.
7. Start/retry the Manager tunnel when configured.
8. Start enabled Always On entries with bounded concurrency.
9. Start native system-tray integration.
10. Show a Gio window unless Start Minimized is enabled.
11. Run updater and Managed idle timers.

Launch-at-login starts the normal native desktop application, not a hidden headless substitute.

## 16. Shutdown sequence

1. Stop accepting new Manager MCP mutations.
2. Cancel application runtime/updater timers.
3. Stop Server Entries with bounded concurrency.
4. Stop Manager tunnel runtime.
5. Flush/close disk logging.
6. Remove the tray icon and terminate the desktop process.
7. Release single-instance ownership after application termination.

External HTTP MCP services are not terminated.

## 17. Security boundaries

- Manager MCP tools accept only Server IDs.
- Manager MCP rejects browser-originated requests.
- There is no browser management interface.
- Manager and focus/MCP local endpoints bind only to loopback as designed.
- Child commands are executed directly, never through a shell.
- Runtime release downloads require SHA-256 verification before use.
- Secret values are never written to JSON configuration and are redacted before retained logging.
- The diagnostics UI is not an interactive shell.

## 18. Windows integration

- Release Windows desktop binaries are linked with `-H=windowsgui` so normal GUI launch does not open a console window.
- Windows secret storage uses Current User DPAPI through `golang.org/x/sys/windows` rather than PowerShell cryptography APIs.
- DPAPI ciphertext remains under Portable Root and is user-bound.
- Native CI includes a Windows DPAPI Put/Get/Delete round trip.
- Tray behavior must be validated on Windows for launch, minimize, close-to-tray, restore, explicit Exit, start-minimized, and second-launch focus handoff.

The Windows GUI subsystem flag is a release-packaging boundary; source/developer/headless builds continue to support diagnostic CLI behavior.

## 19. Verification and CI

Required gates:

- `go mod tidy` produces no diff to committed `go.mod` / `go.sum`.
- `go test ./...`.
- `go vet ./...`.
- Native GUI compilation on Linux, Windows, and macOS.
- Native Windows DPAPI test.
- Headless-compatible builds for Windows/Linux/macOS on amd64 and arm64.
- All required branch and post-merge CI passes before release.
- v1.0.1 release workflow succeeds for six native targets plus changelog, source archives, and `SHA256SUMS.txt`.

Real OpenAI tunnel acceptance remains an operator/release-gated test because CI does not contain production Runtime API keys or permanent tunnel resources. Do not claim that acceptance was executed without suitable operator-provided credentials/resources.
