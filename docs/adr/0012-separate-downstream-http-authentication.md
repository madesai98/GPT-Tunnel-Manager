# Separate downstream HTTP authentication from Manager authentication

Status: accepted

GPT Tunnel Manager continues to separate Manager access control from authentication it performs as an MCP client when a configured HTTP downstream requires it. Downstream HTTP connections support MCP OAuth and a secret-backed static authorization/header mode. Interactive OAuth connect/reconnect is owned by the native desktop UI; OAuth tokens and state are stored under a Server-ID-scoped internal secret namespace, while reusable static credentials may use normal `secret://` references. These credentials never enter configuration JSON, the semantic index, enrichment batches, or Manager MCP calls, and they remain separate from the Manager Tunnel Runtime API key.

Credential-bearing HTTP requires HTTPS except for loopback endpoints. An advanced per-endpoint setting may explicitly allow insecure credential transport for users who intentionally need remote plaintext HTTP; the same transport rule applies to credential-bearing embedding-provider endpoints.