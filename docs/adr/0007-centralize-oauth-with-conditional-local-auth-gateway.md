# ADR 0007: Centralize OAuth with a conditional local Auth Gateway

Status: **Superseded by ADR 0008**.

This ADR recorded an earlier design in which Tunnel Manager would place a Manager-owned loopback Auth Gateway in front of Manager and Server Entry MCP resources configured for Shared OAuth.

The design was intentionally removed before v1 implementation. The final architecture does not centralize end-user authentication and does not add an Auth Gateway. Each MCP server is responsible for its own authentication when needed, while the Manager MCP and each server tunnel use the direct tunnel-client path described in ADR 0008.

This file remains only as historical design context. It must not be used as an implementation requirement.
