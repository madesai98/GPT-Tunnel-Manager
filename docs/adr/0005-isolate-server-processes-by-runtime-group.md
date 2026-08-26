# Isolate Server Entry processes by Runtime Group

Each active Server Entry receives its own OS-level Runtime Group containing every process Tunnel Manager owns for that entry. This boundary must allow Tunnel Manager to stop or restart one Server Entry without affecting another while preserving the stronger invariant that exiting Tunnel Manager cleans up every Manager-owned process.

On Windows, Runtime Groups map to Job Objects configured for child-tree cleanup when ownership closes. On Linux and macOS, they map to process-group/session semantics with graceful termination followed by forced tree cleanup after the configured shutdown timeout.

For Stdio entries, the foreground Tunnel Runtime and its MCP child belong to the same Server Entry Runtime Group. For Managed HTTP entries, both the Manager-launched HTTP MCP process and the foreground Tunnel Runtime belong to that entry's Runtime Group. External HTTP entries place only their Tunnel Runtime in the group because Tunnel Manager does not own the external HTTP service.
