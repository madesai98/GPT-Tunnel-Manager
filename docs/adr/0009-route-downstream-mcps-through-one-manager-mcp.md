# Route downstream MCPs through one Manager MCP

Status: accepted

GPT Tunnel Manager v2 exposes one fixed upstream Manager MCP and connects directly to configured downstream MCP servers as an MCP client. Per-server Secure MCP Tunnels, per-server Developer Plugins, lifecycle markers, and ChatGPT-side lifecycle choreography are retired; `tunnel-client` remains only for the optional Manager Tunnel. This trades direct host exposure of every downstream tool for a stable aggregation boundary that can own routing, lifecycle, indexing, and compatibility behavior centrally.

This supersedes the per-server-plugin topology assumptions in ADR 0004, ADR 0006, and the topology-specific language in ADR 0008 without changing ADR 0008's decision not to add authentication in front of the Manager MCP.