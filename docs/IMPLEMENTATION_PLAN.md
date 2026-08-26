# GPT Tunnel Manager Implementation Plan

Status: planning complete; implementation has not started.

This document is the implementation contract for v1. It consolidates the settled decisions in `CONTEXT.md` and ADRs 0001-0007 into one build sequence. External OpenAI product details are intentionally isolated behind adapters/constants because Secure MCP Tunnel, `tunnel-client`, ChatGPT Plugins, and MCP OAuth surfaces can change independently of this application.

## 1. Product scope

GPT Tunnel Manager is a portable Go desktop application using Gio. It manages local MCP Server Entries, one OpenAI Secure MCP Tunnel per entry, a separate Manager MCP tunnel, lifecycle policy, tunnel-client installation/update, diagnostics, optional centralized OAuth enforcement, and a generic ChatGPT Lifecycle Skill.

V1 supports:

- Windows amd64 and arm64.
- Linux amd64 and arm64.
- macOS amd64 and arm64.
- Server Modes: Always On, Managed, Manual.
- Transport Types: Stdio, Managed HTTP, External HTTP.
- One distinct Tunnel ID per Server Entry plus one Manager Tunnel ID.
- Foreground `tunnel-client run` ownership only.
- A bounded in-memory structured log store and collapsible live console.
- Disk logging only when explicitly enabled.
- No Authentication or Shared OAuth 2.1/OIDC per resource.
- A generic Lifecycle Skill using immutable Server IDs in plugin descriptions.
- A strict Portable Root beside the executable, or beside the `.app` bundle on macOS.

V1 does not:

- Merge multiple MCP servers into one Developer Plugin.
- Reimplement the OpenAI tunnel protocol.
- Create OpenAI tunnels through an admin key by default.
- Install Node.js, Python, Java, Bun, or any MCP server runtime.
- Implement a public OAuth authorization server.
- Allow ChatGPT lifecycle tools to execute arbitrary commands, paths, environment values, or tunnel IDs.
- Treat the live console as an interactive shell.

## 2. External OpenAI contracts

Verified against current OpenAI/tunnel-client documentation on 2026-08-26. Reverify these contracts before implementation and before promoting future tunnel-client versions.

Centralized product-link constants:

- OpenAI Tunnels: `https://platform.openai.com/settings/organization/tunnels`
- Runtime API Keys: `https://platform.openai.com/settings/organization/api-keys`
- Admin API Keys, diagnostic/help only: `https://platform.openai.com/settings/organization/admin-keys`
- Documented ChatGPT connector fallback: `https://chatgpt.com/#settings/Connectors`
- Best-effort ChatGPT Plugins deep link: `https://chatgpt.com/#settings/Plugins`

The UI exposes:

- `Create / Manage Tunnels`
- `Create Runtime API Key`
- `Open ChatGPT Developer Plugins`

The first two use the documented Platform destinations. The ChatGPT button uses the best-effort Plugins deep link and always shows fallback text such as `ChatGPT Settings -> Plugins`. All links live in one `productlinks` package and nowhere else in UI code.

Current tunnel runtime key expectations:

- Runtime key: Tunnels Read + Use.
- Admin key: needed only for tunnel CRUD.
- Core runtime concepts: Tunnel ID, runtime API key, and either an MCP server URL or MCP command.
- The same runtime key may be referenced by multiple Server Entries when its principal can use all corresponding tunnels.

The app never persists an admin key for normal v1 operation.

## 3. High-level architecture

```text
                                +-------------------------+
                                |      ChatGPT            |
                                | Manager Developer Plugin|
                                +-----------+-------------+
                                            |
                                      Manager Tunnel
                                            |
                                      tunnel-client
                                            |
                         no auth ------------+------------ Shared OAuth
                            |                              |
                    Manager MCP                    Manager Auth Gateway
                    loopback HTTP                         |
                            |                         Manager MCP
                            +------------------------------+


ChatGPT Participating Developer Plugin
                 |
          Server Tunnel
                 |
          tunnel-client
                 |
       +---------+------------------------------------+
       |                                              |
   No Authentication                              Shared OAuth
       |                                              |
 direct MCP binding                           loopback Auth Gateway
 Stdio / HTTP                                        |
                                              underlying MCP target
                                      Stdio / Managed HTTP / External HTTP
```

There is no central multi-server MCP endpoint. Each target Developer Plugin continues to expose only its own MCP server's tools.

### Direct no-auth path

No-auth Server Entries use the lightest possible path:

- Stdio: `tunnel-client` owns the stdio MCP child.
- Managed HTTP: Tunnel Manager owns the HTTP process; `tunnel-client` connects directly to its local endpoint.
- External HTTP: `tunnel-client` connects directly to the configured external/local endpoint.

Managed Activity on this path comes from structured tunnel-client telemetry as defined by ADR 0002.

### Shared OAuth path

When the effective Authentication Policy is Shared OAuth, `tunnel-client` connects to a Manager-owned loopback Auth Gateway instead of directly to the underlying MCP server.

The Auth Gateway:

- Presents the MCP HTTP surface to tunnel-client.
- Exposes OAuth protected-resource metadata/challenges needed by the MCP authorization flow.
- Validates bearer tokens before forwarding MCP traffic.
- Checks issuer, expiry/not-before, resource/audience, and configured scopes.
- Never logs the bearer token.
- Emits typed Managed Activity events after authenticated meaningful MCP work.
- Forwards the request to the configured underlying MCP target.
- Is not a general-purpose HTTP reverse proxy.

For Stdio + Shared OAuth, Tunnel Manager owns the stdio process and the MCP client session because `tunnel-client` must point at the HTTP Auth Gateway. Stdio protocol stdout is reserved for MCP and is never consumed as ordinary process logs.

The authorization server remains externally reachable HTTPS infrastructure provided by an established OAuth/OIDC provider. Tunnel Manager is the resource server/gateway, not the authorization server.

## 4. Authentication model

Global authentication modes:

- `none`
- `shared_oauth`

Per-Server Entry policy:

- `inherit`
- `none`
- `shared_oauth`

The Manager MCP has its own `require_authentication` toggle independent of the Server Entry default.

All protected resources may share the same OAuth/OIDC issuer, signing keys, login session, and identity account, but they must not share one literal access token. Each tunnel-backed MCP resource uses its own resource/audience binding.

V1 token validation target:

- OAuth 2.1/OIDC provider with discovery metadata.
- JWT access tokens validated through provider JWKS.
- PKCE/client registration/linking is handled by ChatGPT and the provider, not Tunnel Manager.
- Opaque-token introspection is deferred unless implementation testing shows a required provider cannot issue suitable JWT access tokens.

Each protected resource derives its canonical resource identifier from the current tunnel-backed MCP resource. Resource generation belongs in `internal/auth/resource` and is never hand-assembled by UI code.

The Auth Gateway validates, in order:

1. Bearer header structure.
2. Signature and key ID against cached JWKS.
3. `iss` against configured issuer.
4. `exp` and `nbf` with a small configured clock-skew allowance.
5. Expected resource/audience for that Manager or Server Entry.
6. Required scopes.

JWKS and discovery metadata are cached with bounded TTL and refreshed on unknown key ID or expiry. Authentication failures become sanitized structured log events and MCP-compatible authorization responses.

## 5. Repository/package layout

```text
cmd/
  tunnel-manager/
    main.go
internal/
  app/                 # composition root, startup/shutdown
  config/              # schemas, validation, migration, atomic persistence
  secrets/             # SecretStore abstraction and redaction registration
  manager/             # Manager MCP service and app-wide orchestration
  servers/             # Server Entry definitions and supervisors
  lifecycle/           # desired/observed state machine, retries, idle logic
  process/             # Runtime Group and ManagedProcess abstractions
  tunnel/              # tunnel runtime model and readiness
  tunnelclient/        # installation, version adapter, profile generation
  auth/
    gateway/            # conditional loopback MCP Auth Gateway
    oauth/              # discovery/JWKS/JWT validation
    resource/           # resource/audience derivation
  mcp/                 # transport adapters and MCP protocol glue
  updater/             # tunnel-client release/checksum/promotion/rollback
  events/              # typed application event bus
  logging/             # levels, redactor, ring store, disk sink/export
  productlinks/        # OpenAI/ChatGPT browser destinations
  instance/            # single-instance lock/focus IPC
  ui/
    app/
    pages/
    components/
    dialogs/
    console/
    theme/
  platform/
    windows/
    linux/
    darwin/
assets/
  lifecycle-skill/
    SKILL.md            # embedded/exportable generic skill text
docs/
  adr/
  IMPLEMENTATION_PLAN.md
```

Platform packages use build tags. Higher layers depend only on small interfaces.

## 6. Persisted configuration

All ordinary app files live below Portable Root. Configuration files contain references to secrets, never secret values.

### `config/manager.json`

```json
{
  "schema_version": 1,
  "manager_tunnel": {
    "tunnel_id": "tunnel_...",
    "runtime_credential_ref": "secret://openai/runtime/default"
  },
  "authentication": {
    "mode": "none",
    "manager_require_authentication": false,
    "default_server_policy": "inherit",
    "allow_server_overrides": true,
    "shared_oauth": {
      "issuer": "",
      "discovery_url": "",
      "required_scopes": [],
      "clock_skew_seconds": 60
    }
  },
  "general": {
    "launch_at_startup": false,
    "start_minimized": false,
    "minimize_to_tray": true,
    "close_behavior": "minimize",
    "confirm_exit": true
  },
  "managed_defaults": {
    "idle_timeout_seconds": 300
  },
  "logging": {
    "capture_level": "info",
    "display_level": "info",
    "memory_limit_mb": 25,
    "write_to_disk": false,
    "disk_minimum_level": "debug",
    "maximum_file_size_mb": 10,
    "keep_files": 5
  },
  "tunnel_client": {
    "auto_update": true,
    "channel": "stable",
    "update_check_interval_hours": 24
  },
  "appearance": {
    "theme": "system"
  }
}
```

`default_server_policy` resolves as follows:

- If global auth mode is `none`, inherited entries are no-auth.
- If global auth mode is `shared_oauth`, inherited entries require Shared OAuth.
- Explicit per-entry overrides win only when `allow_server_overrides` is true.

### `config/servers.json`

```json
{
  "schema_version": 1,
  "servers": [
    {
      "id": "srv_...",
      "name": "Skill Tree Maker",
      "chatgpt_plugin_name": "Skill Tree Maker",
      "enabled": true,
      "mode": "managed",
      "transport": {
        "type": "stdio",
        "stdio": {
          "executable": "C:/path/to/program.exe",
          "args": ["--mcp"],
          "working_directory": "C:/path/to"
        }
      },
      "tunnel": {
        "tunnel_id": "tunnel_...",
        "runtime_credential_ref": null
      },
      "authentication": {
        "policy": "inherit",
        "required_scopes": []
      },
      "environment": {
        "values": {},
        "secret_refs": {}
      },
      "runtime": {
        "startup_timeout_seconds": 30,
        "shutdown_timeout_seconds": 10,
        "idle_timeout_seconds": null
      },
      "logging": {
        "capture_level_override": null
      }
    }
  ]
}
```

Transport-specific payloads:

Stdio:

```json
{
  "type": "stdio",
  "stdio": {
    "executable": "/absolute/or/resolved/path",
    "args": ["arg1", "arg2"],
    "working_directory": "/path"
  }
}
```

Managed HTTP:

```json
{
  "type": "managed_http",
  "managed_http": {
    "url": "http://127.0.0.1:9000/mcp",
    "launch": {
      "executable": "/path/to/server",
      "args": ["--port", "9000"],
      "working_directory": "/path"
    }
  }
}
```

External HTTP:

```json
{
  "type": "external_http",
  "external_http": {
    "url": "http://127.0.0.1:9000/mcp"
  }
}
```

Commands are always executable + argument array. There is no persisted shell-command string.

### Configuration persistence rules

- Validate the complete prospective configuration before writing.
- Write to a temporary sibling file, flush, then atomically replace.
- Keep a last-known-good backup during schema migration.
- A future schema version that cannot be understood must fail clearly rather than being silently rewritten.
- Runtime-only data is not authoritative configuration and is never trusted after restart.

## 7. Secret storage

Config stores identifiers such as:

```text
secret://openai/runtime/default
secret://server/srv_.../env/MY_TOKEN
```

`SecretStore` interface:

```go
type SecretStore interface {
    Put(ctx context.Context, ref SecretRef, value []byte) error
    Get(ctx context.Context, ref SecretRef) ([]byte, error)
    Delete(ctx context.Context, ref SecretRef) error
}
```

Platform implementations:

- Windows: DPAPI-protected secret material/references.
- macOS: Keychain.
- Linux: Secret Service/keyring when available, with an explicit unsupported/locked diagnostic rather than plaintext fallback.

Moving the Portable Root to another machine may require secret re-entry. The UI must explain that portable configuration does not mean portable decrypted credentials.

Every secret written or loaded is registered with the central log redactor before it can reach the event/logging pipeline.

## 8. Portable filesystem layout

```text
TunnelManager/
  TunnelManager.exe             # Windows example
  config/
    manager.json
    servers.json
  data/
    instance/
    tunnel-client/
      profiles/
      state/
    cache/
  tools/
    tunnel-client/
      <version>/
        tunnel-client[.exe]
      active.json
  logs/                         # empty/absent unless disk logging or export is used
    manager/
    servers/
      <server-id>/
```

macOS uses the directory containing the `.app` bundle as Portable Root. The executable payload remains one compiled Go application; the `.app` wrapper may contain the minimal metadata required for desktop integration.

If Portable Root is not writable, startup fails with a clear error. There is no silent fallback to AppData, Library, XDG config, registry-backed configuration, or another application-data directory.

## 9. Runtime ownership

Each active Server Entry gets one independent Runtime Group. The Manager tunnel gets its own Runtime Group.

Windows:

- Create a Job Object per Runtime Group.
- Enable kill-on-job-close semantics.
- Assign Manager-owned direct children before they can escape the ownership boundary.
- Child processes spawned by tunnel-client remain in the same job unless a verified tunnel-client version requires a documented exception.

Linux/macOS:

- Create one process group/session per Runtime Group.
- Send graceful termination to the group.
- After the configured shutdown timeout, force-kill the entire group.

Runtime Group contents:

- No-auth Stdio: foreground tunnel-client + its stdio MCP descendant.
- OAuth Stdio: foreground tunnel-client + Manager-launched stdio MCP process.
- Managed HTTP: foreground tunnel-client + Manager-launched HTTP process.
- External HTTP: foreground tunnel-client only.

The in-process Auth Gateway is not a child process but is registered with the same logical Server Entry supervisor and is shut down before its Runtime Group is destroyed.

## 10. Lifecycle model

Observed states exposed to UI/MCP:

- `stopped`
- `starting`
- `ready`
- `degraded`
- `retry_wait`
- `stopping`

Internal start phases may be more detailed:

```text
Preflight
-> StartingOwnedServer      (when applicable)
-> StartingAuthGateway      (when applicable)
-> StartingTunnel
-> Probing
-> Ready
```

The UI normally shows the coarser `Starting` state plus the current phase in details/logs.

### Desired State by mode

Always On:

- Desired State is Running whenever Tunnel Manager is active and the entry is enabled.
- Unexpected failure triggers bounded retry/backoff while the desired state remains Running.
- ChatGPT may observe/wait but may not mutate it.

Managed:

- Starts Stopped after a full Tunnel Manager restart.
- UI or Manager MCP may set Desired State to Running/Stopped.
- Meaningful MCP work resets idle timeout.
- Idle timeout stops the entry when no meaningful work occurs.
- While Desired State is Running, crash recovery uses bounded retry/backoff.

Manual:

- Starts Stopped after a full Tunnel Manager restart.
- Only the desktop UI may change Desired State.
- ChatGPT may use it when already Ready but cannot start, restart, or stop it.
- Unexpected failure is surfaced; it is not automatically brought back up by ChatGPT.

Disabled:

- Forces Desired State to Stopped regardless of mode.
- Lifecycle starts fail until the user re-enables the entry in the UI.

### Retry/backoff

The supervisor must never hot-loop. Initial v1 retry schedule for auto-recovering desired-running entries:

```text
1s, 2s, 5s, 10s, 30s, then 60s capped
```

A successful Ready interval resets the short backoff sequence. Repeated failure leaves the entry `Degraded` between retry windows while preserving Always On/Managed desired-running intent. The exact retry clock is owned by `internal/lifecycle`, not tunnel-client.

### Serialization and idempotency

Lifecycle mutations are serialized per Server Entry.

- `start` when Ready: success with `already_running=true`.
- `shutdown` when Stopped: success with `already_stopped=true`.
- `restart`: serialized stop then start.
- Concurrent duplicate calls join or observe the same transition instead of spawning duplicate runtimes.

Editing a running entry never silently mutates a live tunnel binding. Changes that affect process, transport, tunnel, credentials, or auth mark the entry as requiring restart/reconnect; the UI presents the explicit action.

## 11. Readiness

A PID is never sufficient for Ready.

Readiness requires:

- Required owned MCP process is alive, when applicable.
- Auth Gateway is listening and healthy, when applicable.
- Foreground tunnel-client process is alive.
- tunnel-client `/readyz` is healthy.
- Required target connectivity has passed the selected adapter's readiness probe.

Tunnel Manager records the resolved health/admin URL produced by the launched tunnel-client runtime so it can query `/healthz` and `/readyz` without fixed ports.

`get_status(wait_for_ready=true)` waits on typed state transitions, not log parsing.

- Default wait: 30 seconds.
- Maximum requested wait: 60 seconds.
- A timeout returns the current status snapshot and a sanitized timeout result; it does not cancel the underlying startup attempt.

## 12. Manager MCP contract

The Manager MCP exposes exactly four lifecycle tools.

### `get_status`

Input:

```json
{
  "server_id": "srv_...",
  "wait_for_ready": false,
  "timeout_seconds": 30
}
```

`server_id` may be omitted to list all Server Entries. `wait_for_ready` is valid only when one `server_id` is supplied. Requested timeout is clamped to 60 seconds.

Single-entry result shape:

```json
{
  "server": {
    "server_id": "srv_...",
    "name": "Skill Tree Maker",
    "enabled": true,
    "mode": "managed",
    "desired_state": "running",
    "observed_state": "ready",
    "phase": "ready",
    "ready": true,
    "tunnel_ready": true,
    "authentication_policy": "none",
    "idle_shutdown_enabled": true,
    "activity_tracking": "tunnel_client_telemetry",
    "last_activity_at": "2026-08-26T19:00:00Z",
    "retry_after_ms": null,
    "last_error": null
  }
}
```

`last_error` contains only a stable code, sanitized message, and retryability flag. It never contains secrets, raw Authorization headers, full environment maps, or arbitrary child command lines.

### `start`

Input:

```json
{"server_id":"srv_..."}
```

Allowed only for enabled Managed entries. No arbitrary executable/path/tunnel arguments exist.

### `restart`

Input:

```json
{"server_id":"srv_..."}
```

Allowed only for enabled Managed entries.

### `shutdown`

Input:

```json
{"server_id":"srv_..."}
```

Allowed only for Managed entries.

Mutation attempts against Always On or Manual entries return a stable `mode_not_mcp_controllable` error. Disabled entries return `server_disabled`.

## 13. Lifecycle Marker and Skill

Every Participating Developer Plugin includes this exact description block:

```text
Managed by GPT Tunnel Manager.
GTM_SERVER_ID=<server-id>
Follow the GPT Tunnel Manager Lifecycle Skill before using this plugin.
```

No name prefix is required. Plugin display names can be anything the user wants.

The Manager Developer Plugin does not carry this marker.

### Lifecycle Skill algorithm

Before using a Participating Developer Plugin:

1. Parse the exact `GTM_SERVER_ID` line.
2. Call Manager MCP `get_status` for that Server ID.
3. If the Manager Developer Plugin is unreachable:
   - Tell the user GPT Tunnel Manager must be started.
   - Do not call the target Developer Plugin.
   - Do not continue with unrelated work in the same assistant response.
   - End the response so the user can start Tunnel Manager before continuing.
4. If the Server Entry is disabled, tell the user it must be enabled in Tunnel Manager and stop the plugin-use flow.
5. If mode is Always On:
   - Never call `start`, `restart`, or `shutdown`.
   - If Ready, proceed.
   - Otherwise call `get_status(wait_for_ready=true)` and wait in bounded calls.
   - If it remains unavailable, report the sanitized state/error and do not invoke the target plugin.
6. If mode is Manual:
   - If Ready, proceed.
   - Otherwise tell the user to start it manually in Tunnel Manager and do not invoke the target plugin.
7. If mode is Managed:
   - Ready: proceed.
   - Stopped: call `start`, then wait for Ready.
   - Starting or Retry Wait: wait for Ready.
   - Degraded: call `restart` once for the current preflight attempt, then wait for Ready.
   - Stopping: wait for Stopped, call `start`, then wait for Ready.
8. Only after Ready, invoke the target Developer Plugin.
9. If the Skill started a Managed entry and the task is clearly complete, it may call `shutdown`; otherwise the Managed idle timeout is the fallback cleanup mechanism.

The Skill never contains a registry of server names or IDs.

## 14. Managed Activity and idle shutdown

Meaningful activity includes actual MCP request work such as tool calls and other application-level requests. Initialization, transport keepalives, health probes, OAuth discovery, and routine session chatter do not reset the idle clock.

Sources:

- No-auth direct path: structured tunnel-client dispatcher telemetry.
- Shared OAuth path: typed Auth Gateway request-completion events.

If a tunnel-client update changes direct-path telemetry so activity classification cannot be proven, idle shutdown is disabled for affected entries and a warning is surfaced. The app never guesses and risks shutting down an active server.

## 15. Tunnel-client installation and updates

`tunnel-client` is downloaded into `tools/tunnel-client/<version>/` and is never embedded into the application binary.

Update flow:

1. Query the official GitHub release metadata.
2. Select exact OS/architecture asset.
3. Download to a temporary file under Portable Root.
4. Verify SHA-256 from release asset digest or official checksum file.
5. Extract into a new version directory.
6. Run compatibility probes through the version adapter.
7. Mark the version active atomically for future launches.
8. Existing runtimes keep their currently running binary until restarted.
9. Retain at least the previous known-good version for rollback.
10. Delete old unused versions only after they are not referenced by a running Runtime Group.

Compatibility probes include:

- Executable starts and reports a parseable version.
- Required foreground `run`/profile behavior is present.
- Required health/readiness surface is present.
- Generated profile validates with `doctor` or the current equivalent.
- Direct-path activity telemetry compatibility is classified.

An otherwise valid update may still be promoted when only activity telemetry compatibility is unknown; affected direct-path Managed entries have idle shutdown disabled until compatibility is restored.

All CLI names/flags/profile fields are isolated inside `internal/tunnelclient` version adapters so OpenAI CLI changes do not spread through application code.

## 16. Structured logging

Five application levels:

- TRACE
- DEBUG
- INFO
- WARN
- ERROR

Every retained event contains:

- timestamp
- level
- source (`Manager` or Server ID/display name)
- component
- message
- structured fields
- optional sanitized error chain

Core components:

- Application
- Lifecycle
- Process
- MCP
- Auth
- Gateway
- Tunnel
- Tunnel Client
- Updater
- Configuration
- Secrets
- Platform
- UI

Default retained capture level is INFO. Internal tunnel-client DEBUG telemetry required for lifecycle logic may still be consumed by the supervisor without being inserted into the retained ring.

The in-memory log store is a bounded ring with configurable budgets:

- 5 MB
- 10 MB
- 25 MB default
- 50 MB
- 100 MB

Oldest retained events are discarded first.

### Redaction

Redaction happens before any event enters memory.

Redact at minimum:

- OpenAI runtime API keys.
- OAuth bearer/access/refresh tokens.
- Authorization headers.
- Secret environment values.
- Configured credentials and secret-store values.

No log level bypasses redaction.

### Child process output

- Ordinary child stdout/stderr may be captured as process events.
- `stderr` is not automatically WARN; it is metadata `stream=stderr` unless a recognized prefix or app-generated event gives it severity.
- Stdio MCP stdout is protocol data and is never treated as console output.
- Raw MCP JSON-RPC payload logging is off by default.
- TRACE may contain sanitized request summaries, method names, durations, and outcomes, not secrets/raw bearer tokens.

### Disk logging

Disabled by default. When enabled, use bounded rotation/retention from settings. There are no hidden diagnostic disk logs when this option is off.

Manual exports:

- Copy selected/visible rows.
- Save current logs as text.
- Save current logs as JSONL.
- Clear in-memory logs.

## 17. Typed event bus

Runtime state is never inferred by parsing human log text.

Typed events include:

```text
ServerStarting
ServerReady
ServerStopping
ServerStopped
ServerCrashed
ServerRetryScheduled
TunnelStarting
TunnelReady
TunnelDisconnected
AuthGatewayStarting
AuthGatewayReady
AuthSucceeded
AuthFailed
ManagedActivityObserved
TunnelClientUpdateAvailable
TunnelClientUpdated
```

Flow:

```text
Runtime subsystems
  -> typed Event Bus
       -> lifecycle/state reducers
       -> UI models
       -> optional notifications
       -> structured Logger
            -> redactor
            -> memory ring
            -> optional disk sink
```

## 18. Gio desktop UI

The UI is compact, technical, and cross-platform. Gio is the only desktop UI framework.

### Main screen

Top Manager section:

- Manager tunnel status.
- Manager auth status.
- tunnel-client version/update status.
- Settings button.

Server table/cards:

- Name.
- Optional ChatGPT Developer Plugin name.
- Mode.
- Enabled state.
- Observed State.
- Tunnel status.
- Auth policy/status.
- Start/Stop/Restart controls appropriate to mode.
- Overflow menu: Edit, Open Logs, Copy Lifecycle Marker, Delete.

Bottom live console:

- Collapsed, Compact, Expanded.
- Draggable divider where Gio layout permits.
- Collapsed bar shows WARN/ERROR counts.
- Filters: source, level, component, search.
- Follow latest toggle.
- Clear/export actions.
- Read-only; no command input.

### Server editor

General:

- Name.
- ChatGPT Developer Plugin name, informational only.
- Enabled.
- Mode.
- Read-only generated Server ID with Copy button.
- Copy Lifecycle Marker button.
- Open ChatGPT Developer Plugins button.

MCP:

- Transport Type.
- Stdio executable/args/cwd.
- Managed HTTP URL + launch executable/args/cwd.
- External HTTP URL.
- Environment and secret environment references.

Tunnel:

- Tunnel ID.
- Global runtime key or per-server override.
- `Create / Manage Tunnels` button.
- `Create Runtime API Key` button.

Authentication:

- Inherit global.
- Shared OAuth.
- No Authentication.
- Required scopes.
- Effective policy/status preview.

Runtime:

- Startup timeout.
- Shutdown timeout.
- Managed idle timeout.
- Current restart/reconnect requirement.

Logs:

- Optional capture-level override.

### Settings

General:

- Launch at startup.
- Start minimized.
- Minimize to tray.
- Close behavior: minimize or exit.
- Confirm exit.

Manager Tunnel:

- Tunnel ID.
- Runtime API key secret reference/editor.
- Current status.
- Platform setup buttons.

Authentication:

- Mode: No Authentication / Shared OAuth.
- Issuer/discovery URL.
- Provider validation status.
- Manager Plugin require-auth toggle.
- Default Server Entry policy.
- Allow per-server overrides.
- Required default scopes.

Managed Servers:

- Default idle timeout.

Tunnel Client:

- Installed version.
- Latest known version.
- Auto-update toggle.
- Check now.
- Roll back when a previous version exists.

Logging:

- Capture level.
- Memory budget.
- Disk logging toggle and rotation settings.

Appearance:

- System / Light / Dark.

### Explicit exit

Closing the window follows the configured close behavior. Explicit `Exit Tunnel Manager` shows, by default:

```text
Exit GPT Tunnel Manager?

This will disconnect all tunnels and stop all
MCP servers currently owned by Tunnel Manager.

[Cancel] [Exit]
[ ] Don't ask again
```

The checkbox disables future confirmations and can be restored in Settings.

## 19. Tray and OS integration

A thin platform adapter owns:

```go
type Platform interface {
    SetLaunchAtStartup(ctx context.Context, enabled bool) error
    LaunchAtStartupEnabled(ctx context.Context) (bool, error)
    ShowTray(ctx context.Context, model TrayModel) error
    UpdateTray(ctx context.Context, model TrayModel) error
    Notify(ctx context.Context, n Notification) error
    OpenURL(ctx context.Context, rawURL string) error
    OpenFolder(ctx context.Context, path string) error
}
```

Tray menu minimum:

- Open Manager.
- Status summary.
- Exit Tunnel Manager.

Optional notification: Always On entry enters sustained Degraded state.

Gio does not define the complete tray/startup/keychain surface, so these remain build-tagged adapters with minimal dependencies.

## 20. Single-instance behavior

Only one Tunnel Manager instance may own a Portable Root.

- Acquire an exclusive lock below `data/instance/`.
- First instance exposes a minimal local focus IPC endpoint and records only non-secret connection metadata under the locked instance directory.
- Second launch requests the existing instance to bring its Gio window forward, then exits.
- If focus IPC is unavailable but the lock owner is alive, the second process exits with a clear message rather than starting a competing supervisor.
- Stale instance metadata is reconciled only after the OS lock proves no owner exists.

## 21. Startup sequence

1. Resolve executable and Portable Root.
2. Verify Portable Root is writable.
3. Acquire single-instance ownership.
4. Initialize redaction/logger/event bus.
5. Load and migrate configuration.
6. Initialize SecretStore and resolve required secret references.
7. Ensure a compatible tunnel-client binary exists.
8. Initialize auth provider metadata/JWKS cache when configured.
9. Start Manager MCP loopback endpoint.
10. If Manager Shared OAuth is enabled, start Manager Auth Gateway.
11. Start Manager foreground Tunnel Runtime and wait for readiness.
12. Start enabled Always On Server Entries with bounded startup concurrency.
13. Create Gio window/tray according to startup settings.
14. Begin updater and idle-management timers.

A failure before the UI can open must still produce a native/dialog or stderr-visible actionable error and clean up any already-created Runtime Groups.

## 22. Shutdown sequence

1. Stop accepting new Manager MCP lifecycle mutations.
2. Mark application shutting down and stop updater/idle timers.
3. Stop Server Entries concurrently with a bounded concurrency limit.
4. For each entry: stop tunnel traffic, close Auth Gateway route/session if present, request graceful owned server termination, then force its Runtime Group after timeout if needed.
5. Stop Manager Tunnel Runtime.
6. Stop Manager Auth Gateway if present.
7. Stop Manager MCP endpoint.
8. Flush optional disk logs and config writes.
9. Close SecretStore/platform resources.
10. Close Runtime Group ownership handles as final OS-level orphan cleanup.
11. Release single-instance ownership.

External HTTP MCP processes are never terminated.

Deleting an active Server Entry performs the same per-entry graceful stop/force cleanup before deleting its config, secret references, runtime artifacts, and optional logs.

## 23. Security boundaries

Manager MCP lifecycle tools accept only configured Server IDs. They never accept:

- executable paths
- arguments
- shell commands
- working directories
- environment variables
- secret values
- Tunnel IDs

This prevents a remote ChatGPT tool call from becoming arbitrary local code execution.

Other boundaries:

- Auth Gateway binds only to loopback.
- Manager MCP binds only to loopback.
- Local admin/focus IPC is not a remote management API.
- Raw child/MCP payloads do not enter logs by default.
- Secrets are redacted before memory.
- Tunnel runtime key authentication is separate from end-user OAuth.
- The console is diagnostics-only.

## 24. Test strategy

### Unit tests

- Config validation and migrations.
- Server ID immutability.
- Mode/Desired State reducer.
- Serialized idempotent lifecycle calls.
- Backoff clock.
- Idle timeout classification.
- Lifecycle Marker parser/generator.
- Manager MCP input rejection.
- Resource/audience derivation.
- JWT/JWKS validation and cache rotation.
- Log redaction and ring eviction.
- Product-link centralization.
- tunnel-client version adapter parsing.

### Integration tests

Use fake MCP servers and a fake/fixture tunnel-client where possible.

- No-auth Stdio lifecycle.
- OAuth Stdio through Auth Gateway.
- Managed HTTP lifecycle.
- External HTTP ownership boundary.
- Runtime Group process-tree cleanup.
- Child crash/retry behavior.
- Readiness transitions.
- Manager MCP wait timeout.
- Direct-path telemetry compatibility loss disables idle shutdown.
- Auth Gateway activity resets idle timeout.
- Config edit marks restart required.
- Delete while active cleans all owned resources.

### Platform tests

Run on each OS:

- Job Object/process-group cleanup.
- Single-instance lock.
- Startup integration enable/disable.
- Tray open/exit.
- URL/folder opening.
- SecretStore round trip.

### Security regression tests

- Attempts to inject command/env/path values through Manager MCP fail schema validation.
- Known secrets never appear in retained logs or exports.
- Authorization headers never appear at TRACE.
- Wrong issuer/audience/scope/signature is rejected.
- A token for Server A is rejected by Server B.
- Manager-unavailable Lifecycle Skill fixture terminates before target plugin use.

### Real OpenAI acceptance tests

Not required for ordinary CI. A manual/release-gated suite uses disposable tunnels and verifies:

- Manager Developer Plugin discovery.
- Participating Developer Plugin direct no-auth path.
- Shared OAuth path with a test IdP.
- Lifecycle Skill preflight behavior.
- Current tunnel-client readiness/telemetry assumptions.

No production secret is committed to fixtures.

## 25. CI and release matrix

Build targets:

```text
windows/amd64
windows/arm64
linux/amd64
linux/arm64
darwin/amd64
darwin/arm64
```

CI stages:

1. `go fmt`/static analysis.
2. Unit tests with race detection where supported.
3. Platform-independent integration tests.
4. Native OS process-ownership tests.
5. Six release builds.
6. Smoke-run/version check of produced artifacts on matching runners or architecture emulation where necessary.
7. SHA-256 generation for release artifacts.

Prefer native GitHub runners for each OS/architecture when available. Where a native hosted runner is unavailable, use a verified cross-build only for dependencies that support it; otherwise use a controlled self-hosted/emulated release runner rather than silently dropping the architecture.

Release artifacts:

- Windows: single `.exe` payload, normally distributed in a `.zip` with checksum metadata.
- Linux: single executable payload, normally distributed in `.tar.gz` plus checksum.
- macOS: executable payload packaged in a minimal `.app` for desktop UX; archive and checksum per architecture.

`tunnel-client` is not bundled into these release artifacts; it is installed/updated into Portable Root on first use.

## 26. Implementation phases

### Phase 1 - Core domain and persistence

- Go module and package skeleton.
- Config schemas/migrations/atomic writes.
- Server IDs.
- SecretStore interfaces.
- Event bus and structured logging/redaction.
- Portable Root and single-instance ownership.

Exit criteria: config round-trips, secrets never enter JSON/logs, second instance cannot compete.

### Phase 2 - Process and tunnel-client substrate

- Runtime Group abstraction for Windows/Linux/macOS.
- tunnel-client downloader/checksum/version adapter.
- profile generation and foreground `run` supervision.
- health/readiness integration.

Exit criteria: one fake/real local Stdio entry can start/stop with zero orphaned processes.

### Phase 3 - Server lifecycle

- Three Transport Types.
- Desired/Observed State reducer.
- retries/backoff.
- Always On/Managed/Manual rules.
- Managed Activity and idle timeout for direct path.

Exit criteria: lifecycle integration suite passes on all supported desktop OS families.

### Phase 4 - Manager MCP and Lifecycle Skill

- In-process Manager MCP HTTP endpoint.
- Four lifecycle tools.
- fixed Lifecycle Marker generation.
- generic embedded/exportable Lifecycle Skill.
- fail-closed preflight fixtures.

Exit criteria: Manager tools cannot escape configured Server IDs and Skill behavior matches ADR 0006.

### Phase 5 - Shared OAuth

- provider discovery/JWKS cache.
- JWT validation.
- resource/audience derivation.
- conditional Auth Gateway.
- Stdio/HTTP forwarding adapters.
- auth logging/activity events.

Exit criteria: resource-bound token for one entry cannot access another; no-auth path remains direct.

### Phase 6 - Gio UI

- Main server list.
- Server editor.
- Settings.
- live collapsible console.
- setup buttons/product links.
- explicit exit confirmation.

Exit criteria: all core operations are possible without editing JSON.

### Phase 7 - Platform UX

- tray.
- launch at startup.
- native URL/folder open.
- notifications.
- focus existing instance.

Exit criteria: minimize/close/exit behavior works consistently on Windows, Linux, and macOS.

### Phase 8 - Updater hardening and release

- scheduled tunnel-client update checks.
- compatibility classification.
- rollback.
- full six-target CI/release jobs.
- manual OpenAI acceptance run.

Exit criteria: reproducible release artifacts and verified tunnel-client promotion/rollback path.

## 27. Implementation guardrails

Before coding any OpenAI-facing adapter, re-check current official OpenAI/tunnel-client documentation for:

- foreground run/profile CLI syntax
- health URL discovery
- release asset/checksum naming
- structured telemetry used for Managed Activity
- tunnel permission names
- ChatGPT Plugin setup navigation
- MCP OAuth protected-resource behavior

Changing those external details should normally require changes only in `internal/tunnelclient`, `internal/productlinks`, or `internal/auth`, not in lifecycle/domain logic.

The uploaded launcher that inspired tunnel-client auto-install contained a literal API credential. It is reference material only. No credential from that file may be copied into source, fixtures, documentation, or generated config; the exposed credential should be rotated outside this project.
