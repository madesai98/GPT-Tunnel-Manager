# Self-identify managed Developer Plugins by Server ID

Managed Developer Plugins will identify themselves in their own ChatGPT Developer Mode description using a standard GPT Tunnel Manager marker plus the immutable Server ID of the corresponding Server Entry. The separately installed Lifecycle Skill remains generic and contains no server-specific names, IDs, or registry.

When ChatGPT is about to use a marked Developer Plugin, the Lifecycle Skill reads the embedded Server ID, queries the Manager MCP with that ID, starts or waits for the Managed Server Entry when necessary, and then proceeds to the target plugin. Plugin display names and optional prefixes are human-facing only and are not authoritative identity.

This creates deterministic mapping without coupling the Skill to a user's changing set of MCP servers. Deleting and recreating a Server Entry retires its old Server ID, so the corresponding Developer Plugin description must be updated to the new ID. The Manager Developer Plugin is excluded from this discovery mechanism to avoid recursive lifecycle handling.
