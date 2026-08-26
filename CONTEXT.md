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
The lifecycle policy assigned to a Server Entry. The supported modes are Always On, Managed, and Manual.
_Avoid_: Startup mode

**Always On**:
A Server Mode whose desired state is running whenever Tunnel Manager is active.

**Managed**:
A Server Mode whose desired state can be controlled through the Manager MCP as well as the desktop UI.
_Avoid_: Automatic

**Manual**:
A Server Mode whose desired state can be changed only through the desktop UI.

**Desired State**:
Whether Tunnel Manager currently intends a Server Entry to be running or stopped, independent of the process's observed runtime state.
_Avoid_: Status

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
