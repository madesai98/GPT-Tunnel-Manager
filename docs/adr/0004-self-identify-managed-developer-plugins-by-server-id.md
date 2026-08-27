# Self-identify participating Developer Plugins by Server ID

Every Developer Plugin associated with a Server Entry identifies itself in its own ChatGPT Developer Mode description with a fixed GPT Tunnel Manager marker and the immutable Server ID of the corresponding Server Entry. Participation does not depend on the entry's current mode, so changing between Always On, Managed, and Manual never requires changing the plugin description.

The required one-line app description is:

```text
GTM PLUGIN | <server-id> | Follow the gpt-tunnel-manager-lifecycle skill before using this plugin
```

The user may place any other description text before or after this block, and the Developer Plugin display name is entirely user-chosen. Tunnel Manager does not require or suggest a naming prefix and does not use names as lifecycle identity.

When ChatGPT is about to use a participating Developer Plugin, the Lifecycle Skill reads the Server ID from the `GTM PLUGIN` app description, queries the Manager MCP with that ID, and follows the lifecycle behavior appropriate to the Server Entry's reported mode and state before proceeding to the target plugin. The separately installed Lifecycle Skill remains generic and contains no server-specific names, IDs, or registry.

Deleting and recreating a Server Entry retires its old Server ID, so the corresponding Developer Plugin description must be updated to the new ID. The Manager Developer Plugin is excluded from this discovery mechanism to avoid recursive lifecycle handling.
