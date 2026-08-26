# ADR 0008: No Manager-Layer Authentication

Status: Accepted

Date: 2026-08-26

## Decision

GPT Tunnel Manager does not add an authentication layer in front of the Manager MCP or participating MCP Server Entries.

The runtime topology is direct:

- Manager Developer Plugin -> Manager Secure MCP Tunnel -> `tunnel-client` -> loopback Manager MCP.
- Participating Developer Plugin -> its Secure MCP Tunnel -> `tunnel-client` -> configured Stdio or HTTP MCP target.

Each individual MCP server remains responsible for whatever authentication its own service needs. Tunnel Manager does not centralize those credentials or impersonate an OAuth authorization/resource server.

The OpenAI Runtime API key is explicitly outside this end-user authentication decision. It is a control-plane credential resolved at runtime and supplied only to `tunnel-client` as `CONTROL_PLANE_API_KEY` so the Secure MCP Tunnel runtime can connect to OpenAI.

## Consequences

- Remove Shared OAuth, issuer/JWKS, token-validation, resource/audience, and Auth Gateway packages from v1 scope.
- Remove authentication-policy fields from persisted Manager and Server Entry schemas.
- Do not add auth settings to the Manager Developer Plugin connection.
- Do not proxy otherwise direct no-auth Stdio or HTTP traffic merely to enforce a centralized identity layer.
- Keep Server Entries independent: one Developer Plugin and one Tunnel ID per MCP server.
- Keep the Manager Developer Plugin minimal: it exposes only the four lifecycle tools.

## Localhost browser protection is separate

The advanced loopback web UI may use a random per-process SameSite session token and same-origin checks to prevent unrelated web pages from issuing localhost mutation requests. The loopback Manager MCP rejects requests containing a browser `Origin` header.

These are local process/browser isolation controls. They are not MCP authentication, are not exposed through the Secure MCP Tunnels, and do not alter the no-auth Manager/plugin architecture.

## Supersedes

This ADR supersedes ADR 0007 and all Shared OAuth/Auth Gateway sections in earlier versions of `docs/IMPLEMENTATION_PLAN.md` and `CONTEXT.md`.
