# Expose routing preference management on the Manager MCP

Status: accepted

GPT Tunnel Manager v2 expands the fixed upstream Manager MCP surface from 17 to 19 tools by adding `get_routing_preferences` and `set_routing_preferences`. Preference reads and mutations remain explicit first-class operations instead of being hidden inside indexing tools. The ten permission-preserving execution classes remain unchanged; the additional tools manage only the separate Routing Preference overlay and Routing Profiles.