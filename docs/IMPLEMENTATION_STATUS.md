# Implementation Status

Date: 2026-08-28

## Current released baseline

`main` was frozen for v2 planning at commit `08366ffbd299177870c10a3446ab9e4dcd35a18e` (`Release v1.0.32`). The released application still uses the v1 per-server tunnel/plugin/lifecycle architecture.

## V2 implementation branch

Branch: `feature/v2-mcp-router`

Status: **implementation in progress; Phase 1 complete and Phase 2 is next**.

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

## Phase status

### Phase 0 — baseline and planning contract

Complete. The v1.0.32 planning baseline was frozen and the existing CI matrix was green before production v2 changes.

### Phase 1 — MCP compatibility spike

Complete at commit `5ef7dcc23c757bcbf54ca7cc590af8c443180839`.

Executable coverage in `internal/mcpcompat` proves:

- direct Stdio, Managed HTTP, and External HTTP client connectivity;
- MCP 2026-07-28 modern negotiation and legacy initialize fallback;
- one URL dispatching modern stateless and legacy stateful sessions;
- paginated `tools/list` plus list-change delivery;
- modern MRTR/input-required handling;
- legacy elicitation, sampling, and roots callbacks;
- cancellation propagation;
- structured/multimedia `CallToolResult` fidelity;
- resource-link follow-up reads;
- downstream OAuth authorize/retry behavior;
- Tasks extension wire discrimination and `tasks/get`, `tasks/update`, and `tasks/cancel` method viability.

`docs/V2_COMPATIBILITY_SPIKE.md` records the compatibility decision. The pinned Go SDK does not natively model the Tasks extension's polymorphic `tools/call -> resultType: "task"` result, so v2 carries a narrow task-aware wire compatibility layer rather than changing the accepted architecture.

CI run `33205279379` completed successfully across tests, vet, headless cross-builds, and native GUI builds.

### Phase 2 — clean v2 config/state foundation

Next. Implement the strict v2-native schema, clean-start major cutover, stable Manager MCP port, embedding/auth/local-protection/profile settings, and routing/preference revision-state interfaces. No v1 conversion or compatibility parsing will be added.

## Clean v2 break

V2 intentionally does not preserve compatibility with v1 configuration or routing data. The implementation initializes clean v2 state rather than carrying v1 compatibility structs, aliases, migration journals, or conversion logic. Existing v1 state may be moved aside as opaque discardable legacy data during the one major-version cutover, but it is not parsed or converted.
