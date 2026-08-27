# GPT Tunnel Manager Context

GPT Tunnel Manager is a portable desktop control plane for running local MCP servers through separate OpenAI Secure MCP Tunnel connections and controlling eligible lifecycles from both the native desktop UI and ChatGPT.

This document defines the settled v1 vocabulary and architecture. ADR 0008 supersedes the earlier Shared OAuth/Auth Gateway proposal.

## Core terms

**Tunnel Manager**  
The cross-platform desktop application that owns Server Entries, Tunnel Runtimes, lifecycle policy, the Manager MCP, local diagnostics, and managed `tunnel-client` installation.

**MCP Server**  
An external MCP implementation registered with Tunnel Manager and exposed through its own tunnel.

**Server Entry**  
Tunnel Manager's persisted identity and configuration for one MCP Server.

**Server ID**  
The immutable generated `srv_...` identifier for one Server Entry. It is the authoritative identity used by Manager MCP lifecycle tools and participating Developer Plugin Lifecycle Markers. Deleting and recreating an entry creates a new ID.

**Server Mode**  
The lifecycle policy for a Server Entry:

- **Always On**: Desired State is Running whenever Tunnel Manager is active and the entry is enabled.
- **Managed**: Desired State may be changed by the desktop UI or Manager MCP.
- **Manual**: Desired State may be changed only by the desktop UI.

**Enabled State**  
Whether a Server Entry is permitted to run. Disabled entries are forced stopped and cannot be started until re-enabled.

**Desired State**  
Whether Tunnel Manager currently intends an entry to be Running or Stopped.

**Observed State**  
The runtime condition Tunnel Manager observes: Stopped, Starting, Ready, Degraded, Retry Wait, or Stopping.

**Managed Activity**  
Meaningful MCP request work that resets a Managed entry's idle timer. Initialization, keepalives, health probes, and routine notification chatter do not count.

## Transport and ownership

**Transport Type**  
How a Server Entry reaches its MCP Server:

- **Stdio**: `tunnel-client` launches the configured executable and communicates over stdio.
- **Managed HTTP**: Tunnel Manager launches and owns an HTTP MCP process; `tunnel-client` connects to the configured local URL.
- **External HTTP**: The HTTP MCP service already exists independently; Tunnel Manager owns only its tunnel runtime and never terminates the external process.

Commands are always persisted as executable plus argument array. There is no shell-command string in configuration and Manager MCP tools cannot supply commands.

**Owned MCP Process**  
An MCP server process launched by Tunnel Manager or its owned tunnel runtime. It is terminated when its Server Entry stops or Tunnel Manager exits.

**Tunnel Runtime**  
A foreground `tunnel-client` process owned by Tunnel Manager for the Manager MCP or one Server Entry.

**Runtime Group**  
The process-tree ownership boundary for one active Server Entry or the Manager tunnel. Unix-like platforms use process-group semantics; Windows uses process-tree termination for Manager-owned descendants. Each Server Entry can be stopped independently and application shutdown cleans all Manager-owned descendants.

## ChatGPT integration

**Manager MCP**  
The loopback MCP service built into Tunnel Manager. It exposes exactly:

- `get_status`
- `start`
- `restart`
- `shutdown`

Lifecycle mutation tools accept only immutable configured Server IDs. They never accept executable paths, arguments, environment variables, secret values, or Tunnel IDs.

The native Servers page also displays a built-in `Manager MCP` row first. It is a UI representation of the Manager service, not a persisted Server Entry, has no ordinary Server ID, and cannot be deleted.

**Developer Plugin**  
A ChatGPT Developer Mode plugin connected to one tunnel. Each MCP Server has its own plugin; Tunnel Manager does not merge server tools into the Manager plugin.

**Manager Developer Plugin**  
The Developer Plugin connected to the Manager tunnel. It exposes only the four Manager MCP lifecycle tools.

**Participating Developer Plugin**  
A per-server Developer Plugin whose description contains the Lifecycle Marker for its Server Entry.

**Lifecycle Marker**  
The standard description block:

```text
GTM PLUGIN | <server-id> | Follow the gpt-tunnel-manager-lifecycle skill before using this plugin
```

The immutable Server ID is authoritative; plugin display names are informational only.

**Lifecycle Skill**  
The separately installed generic ChatGPT Skill in `assets/lifecycle-skill/SKILL.md`. It reads a plugin's Lifecycle Marker, checks the Manager MCP, applies mode-specific lifecycle behavior, waits for Ready, and only then invokes the target plugin. It contains no registry of server-specific names or IDs.

## Authentication and credential boundary

GPT Tunnel Manager v1 adds **no Manager-layer authentication** to the Manager MCP or participating server tunnels.

- Each MCP server is responsible for any authentication its own service requires.
- The Manager MCP is exposed to ChatGPT through its dedicated Secure MCP Tunnel without an additional Tunnel Manager OAuth/Auth Gateway.
- Each server tunnel connects directly to its configured Stdio or HTTP target.
- The OpenAI Runtime API key is a separate control-plane credential used only by `tunnel-client` to establish and operate Secure MCP Tunnels.
- The known Manager Runtime API key uses a fixed internal secret reference, `secret://openai/runtime/default`. The native UI asks only for the key value.
- Downstream tunnel runtimes inherit that Manager key by default; arbitrary secret references remain available for custom downstream secrets and environment values.
- Runtime API keys and secret environment values are stored through platform secret storage or controlled environment overrides and never persisted as plaintext configuration values.

## Portable Root

**Portable Root**  
The writable directory under which Tunnel Manager keeps configuration, runtime data, managed `tunnel-client` versions, instance metadata, and optional logs. Windows/Linux use the executable's directory. macOS resolves to the directory containing the `.app` bundle when packaged that way. Tunnel Manager does not silently fall back to OS application-data directories.

## Native desktop shell

The normal application uses Gio as its only management surface. There is no browser-based management UI.

A notification-area/system-tray icon remains active while the Manager process is running. Minimize and close-to-tray remove the native window from the taskbar without shutting down tunnels or owned MCP processes. `Open Manager` creates/restores the native Gio window; explicit Exit performs coordinated shutdown.

A tiny loopback endpoint used only for single-instance ownership/focus handoff is an implementation detail and is not a management interface or tunneled MCP endpoint.
