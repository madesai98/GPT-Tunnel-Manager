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

**Desired State**:
Whether Tunnel Manager currently intends a Server Entry to be Running or Stopped, independent of the process's Observed State.
_Avoid_: Status, Observed State

**Observed State**:
The current runtime condition Tunnel Manager observes for a Server Entry: Stopped, Starting, Ready, Degraded, Retry Wait, or Stopping.
_Avoid_: Desired State, status

**Managed Activity**:
Meaningful MCP work that resets an active Managed Server Entry's idle timeout; initialization and routine transport or session chatter do not count.
_Avoid_: Any tunnel traffic, keepalive

**Owned MCP Process**:
An MCP server process launched by Tunnel Manager and therefore terminated when Tunnel Manager exits.
_Avoid_: External HTTP MCP Endpoint

**External HTTP MCP Endpoint**:
An already-running Streamable HTTP MCP service that Tunnel Manager did not launch; Tunnel Manager owns its tunnel connection but not the external process lifetime.
_Avoid_: Owned MCP Process

**Manager MCP**:
The MCP server built into Tunnel Manager that exposes the lifecycle tools `get_status`, `start`, `restart`, and `shutdown`.
_Avoid_: Manager Plugin

**Tunnel Runtime**:
A running `tunnel-client` instance owned by Tunnel Manager for the Manager MCP or one MCP Server.
_Avoid_: Tunnel, runtime server

**Developer Plugin**:
A custom Developer Mode plugin created on chatgpt.com that connects ChatGPT to one tunnel and exposes that MCP server's tools.
_Avoid_: Skill, ChatGPT App

**Manager Developer Plugin**:
The Developer Plugin connected to the Manager MCP tunnel.
_Avoid_: Manager Plugin bundle

**Lifecycle Skill**:
A separately installed ChatGPT Skill that teaches ChatGPT to check and control Managed Server Entries through the Manager MCP before using their corresponding Developer Plugins.
_Avoid_: Manager Plugin
