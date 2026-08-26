# Centralize OAuth with a conditional local Auth Gateway

Tunnel Manager centralizes end-user authentication for protected MCP resources by placing a Manager-owned loopback Auth Gateway in front of any Manager or Server Entry configured for Shared OAuth. The gateway validates bearer tokens, issuer, resource/audience, expiry, and scopes, exposes the protected-resource metadata expected by MCP OAuth, and forwards only authenticated MCP traffic to the underlying target. The OAuth authorization server itself remains an external HTTPS service and is not implemented or publicly exposed by Tunnel Manager.

No-auth resources keep the direct `tunnel-client` path and do not pay the cost of an otherwise unnecessary proxy. Shared OAuth resources use the same provider and identity system but distinct resource-bound tokens for each tunnel-backed MCP resource, preventing one plugin's token from being replayed against another.
