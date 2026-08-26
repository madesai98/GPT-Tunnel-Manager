# GPT Tunnel Manager

GPT Tunnel Manager is a portable desktop control plane for running local MCP servers through separate OpenAI Secure MCP Tunnel connections and controlling their lifecycle from both the desktop UI and ChatGPT.

## Language

**Tunnel Manager**:
The cross-platform desktop application that owns Server Entries, Tunnel Runtimes, lifecycle policy, and the Manager MCP.
_Avoid_: Manager app, tunnel app

**MCP Server**:
An external MCP implementation registered with Tunnel Manager and exposed to ChatGPT through its own tunnel.
_Avoid_: Plugin, tool server

**Server Entry**:
Tunnel Manager's persisted identity and configuration for one MCP Server.
_Avoid_: Server profile, MCP profile

**Server ID**:
The immutable generated identifier for one Server Entry. It is the authoritative identity used by Manager MCP lifecycle tools and by Developer Plugins participating in Tunnel Manager lifecycle orchestration. Deleting and recreating a Server Entry creates a new Server ID even if its display name, tunnel, or command are the same.
_Avoid_: Display name, Developer Plugin name, Tunnel ID

**Server Mode**:
The policy that determines how a Server Entry's Desired State may be controlled. The supported modes are Always On, Managed, and Manual.
_Avoid_: Startup mode, runtime state

**Always On**:
A Server Mode whose Desired State is Running whenever Tunnel Manager is active.

**Managed**:
A Server Mode whose Desired State can be controlled through the Manager MCP as well as the desktop UI.
_Avoid_: Automatic

**Manual**:
A Server Mode whose Desired State can be changed only through the desktop UI.

**Enabled State**:
Whether a Server Entry is permitted to run. Disabled forces Desired State to Stopped regardless of Server Mode and prevents lifecycle starts until the entry is re-enabled.
_Avoid_: Desired State, Server Mode

**Desired State**:
Whether Tunnel Manager currently intends a Server Entry to be Running or Stopped, independent of the process's Observed State.
_Avoid_: Status, Observed State

**Observed State**:
The current runtime condition Tunnel Manager observes for a Server Entry: Stopped, Starting, Ready, Degraded, Retry Wait, or Stopping.
_Avoid_: Desired State, status

**Managed Activity**:
Meaningful MCP work that resets an active Managed Server Entry's idle timeout; initialization and routine transport or session chatter do not count.
_Avoid_: Any tunnel traffic, keepalive

**Transport Type**:
How a Server Entry reaches its MCP Server. The v1 values are Stdio, Managed HTTP, and External HTTP.
_Avoid_: Server Mode, Tunnel Type

**Stdio**:
A Transport Type where the configured MCP process communicates over stdio. On a no-auth direct path, `tunnel-client` launches the process; when centralized OAuth is required, Tunnel Manager owns the stdio process behind its Auth Gateway.

**Managed HTTP**:
A Transport Type where Tunnel Manager launches and owns an HTTP MCP process, while the Tunnel Runtime ultimately connects to that process either directly or through the Auth Gateway.
_Avoid_: External HTTP

**Owned MCP Process**:
An MCP server process launched by Tunnel Manager or by a Manager-owned Tunnel Runtime and therefore terminated when Tunnel Manager exits.
_Avoid_: External HTTP MCP Endpoint

**External HTTP**:
A Transport Type where the configured Streamable HTTP MCP service already exists independently of Tunnel Manager. Tunnel Manager owns the Tunnel Runtime but not the external server process.
_Avoid_: Managed HTTP

**External HTTP MCP Endpoint**:
The configured endpoint of an External HTTP Server Entry. Tunnel Manager disconnects its tunnel when stopping or exiting but does not terminate the independently owned HTTP process.
_Avoid_: Owned MCP Process

**Runtime Group**:
The isolated OS-level process ownership boundary for the Manager tunnel or one Server Entry. Each active Server Entry has its own Runtime Group so it can be stopped independently while Manager termination still cleans up all Manager-owned processes. On Windows this maps to a Job Object; on Unix-like systems it maps to process-group/session semantics.
_Avoid_: Server Mode, Tunnel Runtime

**Portable Root**:
The writable filesystem directory under which Tunnel Manager keeps its mutable configuration, data, managed tools, and optional logs. On Windows and Linux it is the directory containing the executable; on macOS it is the directory containing the `.app` bundle.
_Avoid_: Current working directory, OS application-data directory

**Manager MCP**:
The MCP server built into Tunnel Manager that exposes the lifecycle tools `get_status`, `start`, `restart`, and `shutdown`.
_Avoid_: Manager Plugin

**Tunnel Runtime**:
A running foreground `tunnel-client` instance owned by Tunnel Manager for the Manager MCP or one MCP Server.
_Avoid_: Tunnel, runtime server

**Developer Plugin**:
A custom Developer Mode plugin created on chatgpt.com that connects ChatGPT to one tunnel and exposes that MCP server's tools. Its display name is entirely user-chosen and is not part of Tunnel Manager identity or lifecycle mapping.
_Avoid_: Skill, ChatGPT App

**Participating Developer Plugin**:
A Developer Plugin associated with a Server Entry and carrying that entry's Lifecycle Marker. Participation is independent of whether the Server Entry is currently Always On, Managed, or Manual.
_Avoid_: Managed Developer Plugin, name-prefixed plugin

**Lifecycle Marker**:
The standard self-identification in a Participating Developer Plugin's description that identifies GPT Tunnel Manager participation and carries the immutable Server ID. The marker and Server ID are authoritative; no Developer Plugin naming convention or prefix is required or suggested.
_Avoid_: Managed Developer Plugin Marker, heuristic name matching, name prefix

**Manager Developer Plugin**:
The Developer Plugin connected to the Manager MCP tunnel. It is the control-plane plugin and is excluded from Lifecycle Marker discovery to avoid recursion.
_Avoid_: Manager Plugin bundle

**Lifecycle Skill**:
A separately installed, generic ChatGPT Skill that teaches ChatGPT to discover Participating Developer Plugins from their Lifecycle Markers, use the embedded Server ID with the Manager MCP, follow the lifecycle behavior appropriate to the Server Entry's reported mode and state, and only then use the target Developer Plugin. The Skill contains no server-specific registry, names, or IDs.
_Avoid_: Manager Plugin, per-server Skill configuration

**Authentication Policy**:
The effective end-user authentication requirement for the Manager MCP or one Server Entry. The v1 policies are No Authentication and Shared OAuth, with Server Entries able to inherit the global default or override it when overrides are enabled.
_Avoid_: Tunnel runtime API key, MCP process credential

**Shared OAuth**:
The centrally configured OAuth 2.1/OIDC identity system used by every protected Manager or Server Entry resource. Resources share the same provider and identity system but receive resource-bound tokens rather than sharing one literal access token.
_Avoid_: Shared bearer token, tunnel API key

**Auth Gateway**:
A Manager-owned loopback MCP HTTP facade used when Shared OAuth is required. It validates the incoming bearer token and resource policy before forwarding the MCP request to the configured underlying Stdio, Managed HTTP, External HTTP, or Manager MCP target.
_Avoid_: Public authorization server, general reverse proxy
