# Implementation Status

Date: 2026-08-28

## Current released baseline

`main` was frozen for v2 planning at commit `08366ffbd299177870c10a3446ab9e4dcd35a18e` (`Release v1.0.32`). The released application still uses the v1 per-server tunnel/plugin/lifecycle architecture.

## V2 planning branch

Branch: `feature/v2-mcp-router`

Status: **planning complete; production implementation has not started**.

The canonical v2 implementation contract is `docs/V2_IMPLEMENTATION_PLAN.md`. `CONTEXT.md` and ADRs 0009 onward describe the accepted v2 architecture and supersede conflicting v1 topology assumptions where stated.

The v2 direction is:

- one fixed 19-tool Manager MCP;
- direct downstream MCP clients for Stdio, Managed HTTP, and External HTTP;
- optional Manager Secure MCP Tunnel only;
- mandatory generation-based semantic catalog/index;
- agent-driven tool enrichment plus capability reconciliation;
- non-blocking Ambiguity Reviews and persistent Routing Preferences/Profiles;
- ten ToolAnnotation-preserving execution classes;
- generation-bound authenticated Execution Handles;
- router-native Managed lifecycle/use leases;
- downstream OAuth/static authentication as a separate client credential boundary;
- optional local Manager capability protection enabled by default;
- modern stateless plus legacy stateful upstream MCP compatibility where required;
- bridging of tool-required Tasks, resource followups, MRTR, cancellation, and legacy callbacks;
- strict v2-native configuration with no v1-to-v2 conversion code.

## Clean v2 break

V2 intentionally does not preserve compatibility with v1 configuration or routing data. The implementation should initialize clean v2 state rather than carrying v1 compatibility structs, aliases, migration journals, or conversion logic. Existing v1 state may be moved aside as opaque discardable legacy data during the one major-version cutover, but it is not parsed or converted.

## Planning-only repository changes

The current feature branch is expected to differ from `main` only in planning/documentation artifacts until the user explicitly authorizes implementation. No production code should be treated as implemented merely because an ADR or plan describes it.

## Implementation entry gate

Production implementation may begin only after explicit user confirmation. The first implementation phase must re-run/freeze the v1.0.32 baseline and then perform the MCP compatibility spike in `docs/V2_IMPLEMENTATION_PLAN.md` before deeper router/index work.
