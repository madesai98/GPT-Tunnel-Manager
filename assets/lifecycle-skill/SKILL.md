---
name: gpt-tunnel-manager-lifecycle
description: Preflight Developer Plugins managed by GPT Tunnel Manager before using their tools.
---

# GPT Tunnel Manager Lifecycle

When a Developer Plugin description contains `GTM PLUGIN | <server-id> | Follow the gpt-tunnel-manager-lifecycle skill before using this plugin`, treat it as participating in GPT Tunnel Manager lifecycle control. The `<server-id>` segment is the exact immutable Server ID passed to Manager lifecycle tools.

1. Call the Manager Developer Plugin `get_status` for the exact immutable Server ID before using the target plugin.
2. If the Manager Developer Plugin is unreachable, tell the user GPT Tunnel Manager must be running and do not call the target plugin.
3. Disabled entries must be enabled in Tunnel Manager first.
4. Always On entries: never mutate lifecycle. If not ready, use `get_status(wait_for_ready=true)` in bounded calls.
5. Manual entries: if not ready, tell the user to start the entry in Tunnel Manager. Never call Manager `start`, `restart`, or `shutdown` for Manual mode.
6. Managed entries: start when stopped, wait when starting/retry-wait, restart once when degraded, and wait through stopping before starting again.
7. Invoke the target Developer Plugin only after the entry reports Ready.
8. When this workflow started a Managed entry and the task is clearly complete, `shutdown` may be called; otherwise rely on its configured idle timeout.

Never pass commands, paths, environment values, secret values, or tunnel IDs to Manager lifecycle tools. The Manager tools accept Server IDs only.
