# ADR 0008: No Manager-Layer Authentication

Status: Superseded by ADR 0018 for v2 local Manager access

Date: 2026-08-26

## Historical decision

V1 did not add an authentication layer in front of the Manager MCP or participating MCP Server Entries. Its topology used per-server Developer Plugins and Secure MCP Tunnels, and the Manager MCP exposed only four lifecycle tools.

Each individual MCP server remained responsible for its own service authentication. GPT Tunnel Manager did not centralize downstream credentials or impersonate an OAuth authorization/resource server. The OpenAI Runtime API key remained a separate tunnel-client control-plane credential.

## V2 status

ADR 0009 replaces the per-server-plugin topology. ADR 0012 allows GPT Tunnel Manager to authenticate as a downstream MCP client. ADR 0018 supersedes this ADR's absolute no-local-auth conclusion by making installation-scoped capability-token protection for the loopback Manager MCP enabled by default but optional in native settings. GPT Tunnel Manager still does not introduce a Manager OAuth/Auth Gateway.

Browser-Origin rejection remains a separate localhost protection and continues regardless of whether optional local Manager access protection is enabled.

## Supersedes

Historically, this ADR superseded ADR 0007 and the Shared OAuth/Auth Gateway sections in earlier v1 planning documents.