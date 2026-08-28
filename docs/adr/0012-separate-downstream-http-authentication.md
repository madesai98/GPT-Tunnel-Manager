# Separate downstream HTTP authentication from Manager authentication

Status: accepted

GPT Tunnel Manager continues to add no authentication layer in front of the Manager MCP, but v2 may authenticate as an MCP client when a configured HTTP downstream requires it. Downstream HTTP connections support MCP OAuth and a secret-backed static authorization/header mode, with credentials and OAuth state kept separate from the Manager Tunnel Runtime API key. This preserves the no-Manager-auth decision while avoiding authentication regressions when per-server Developer Plugins are removed.