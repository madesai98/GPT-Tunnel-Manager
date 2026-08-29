# Expose routing preference management on the Manager MCP

Status: accepted

GPT Tunnel Manager v2 expands the fixed upstream Manager MCP surface from 17 to 19 tools by adding `get_routing_preferences` and `set_routing_preferences`. Preference reads and mutations remain explicit first-class operations instead of being hidden inside indexing tools. The ten permission-preserving execution classes remain unchanged; the additional tools manage only the separate Routing Preference overlay and Routing Profiles.

`get_routing_preferences` is read-only and closed-world. `set_routing_preferences` is writable, destructive, idempotent, and closed-world because one operation may create, update, disable, or delete persisted rules. Preference mutations use canonical preference identities plus optimistic `expected_preference_revision`; repeating an identical mutation is a no-op, while a stale revision returns `preference_conflict` rather than silently overwriting newer rules.