# Self-identify managed Developer Plugins by Server ID

Developer Plugins participating in GPT Tunnel Manager lifecycle orchestration identify themselves in their own ChatGPT Developer Mode description using a standard GPT Tunnel Manager marker plus the immutable Server ID of the corresponding Server Entry. The separately installed Lifecycle Skill remains generic and contains no server-specific names, IDs, or registry.

When ChatGPT is about to use a marked Developer Plugin, the Lifecycle Skill reads the embedded Server ID, queries the Manager MCP with that ID, and follows the lifecycle behavior appropriate to the Server Entry's reported mode and state before proceeding to the target plugin.

Developer Plugin display names are entirely user-chosen. Tunnel Manager does not require or suggest a naming prefix and does not use names as lifecycle identity. The description marker plus Server ID are authoritative.

This creates deterministic mapping without coupling the Skill to a user's changing set of MCP servers or plugin names. Deleting and recreating a Server Entry retires its old Server ID, so the corresponding Developer Plugin description must be updated to the new ID. The Manager Developer Plugin is excluded from this discovery mechanism to avoid recursive lifecycle handling.
