# Infer direct-path Managed idle activity from tunnel-client telemetry

For Server Entries that use the no-auth direct path, Tunnel Manager will infer meaningful Managed Activity from structured `tunnel-client` dispatcher telemetry rather than inserting an otherwise unnecessary MCP proxy. Managed Tunnel Runtimes may emit DEBUG-level structured telemetry internally even when the user's retained log level is INFO; lifecycle-relevant events are consumed by the supervisor while below-threshold diagnostic events are discarded instead of entering the in-memory log ring.

When Shared OAuth is enabled for a resource, its Manager-owned Auth Gateway is already in the MCP data path and provides authoritative typed activity events instead. This preserves the lightweight direct path for unauthenticated servers while allowing centralized authentication where required.

A newly downloaded and otherwise verified `tunnel-client` runtime may still be promoted when its direct-path activity telemetry is no longer recognized. For affected Managed Server Entries, Tunnel Manager disables idle shutdown until compatible activity detection is restored. It must never apply stale telemetry assumptions or shut down a server when activity detection is uncertain.
