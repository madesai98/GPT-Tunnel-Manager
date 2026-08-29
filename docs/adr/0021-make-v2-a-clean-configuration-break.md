# Make v2 a clean configuration break

Status: accepted

GPT Tunnel Manager v2 does not convert, import, or preserve compatibility with v1 configuration or routing data. The v2 release initializes a new v2-native configuration and data model focused on the single Manager MCP router architecture; legacy per-server plugin, tunnel, lifecycle-skill, and marker configuration is not interpreted by v2. This intentionally trades upgrade continuity for a substantially simpler implementation and removes migration, rollback-compatibility, and legacy-schema code that would otherwise remain in the product solely to support an unreleased architecture transition.