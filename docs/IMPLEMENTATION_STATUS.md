# Implementation Status

Date: 2026-08-28

## Current released baseline

`main` was frozen for v2 planning at commit `08366ffbd299177870c10a3446ab9e4dcd35a18e` (`Release v1.0.32`). The released application still uses the v1 per-server tunnel/plugin/lifecycle architecture.

## V2 implementation branch

Branch: `feature/v2-mcp-router`

Status: **implementation in progress; Phases 1–3 complete, Phase 4 next**.

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

Complete through commit `253376478ac98613d3d6e408a999e070e2251873`.

The v2-native foundation now provides:

- strict v2-only manager/server schemas and validation with no v1 compatibility structs, aliases, conversion, or fallback parsing;
- opaque one-major-version cutover handling for legacy `config/` and relevant `data/` bytes;
- atomic fresh initialization of the `manager.json` + `servers.json` pair, with incomplete/half-created v2 configuration rejected fail-closed;
- a stable persisted local Manager MCP port;
- Local Manager Access Protection enabled by default with its generated capability token stored only in the secret store;
- dedicated embedding/downstream credential references with no raw credential values in configuration;
- deterministic `routing_state_hash`, monotonic diagnostic `routing_revision`, and independent `preference_revision` primitives;
- installation-keyed HMAC fingerprints for routing-relevant secret values;
- an explicit config/secret/routing-state coordinator that marks routing unready before routing-affecting writes and only re-enables it after persisted state is recomputed and reconciled;
- startup reconciliation that detects a crash after a routing-affecting config or secret write and prevents an old semantic generation from being treated as current;
- separation of routing-relevant static/environment secret changes and OAuth account/scope identity from operational embedding, Manager Tunnel, and routine OAuth token rotation.

Phase 2 CI run `33229267135` completed successfully: `go mod tidy` produced zero diff, `go test ./...` and `go vet ./...` passed, all six headless cross-build targets passed, and native Linux/Windows/macOS GUI builds passed.

Phase 2 exit gate is satisfied: fresh v2 configuration persists and validates atomically, released v1 configuration is not interpreted, and the fail-closed routing-state mutation boundary is in place before catalog persistence lands in Phase 4.

### Phase 3 — direct downstream MCP clients

Complete through commit `407935c0dce1c7b8e3416bf7de85115089395c1d`.

`internal/downstream` now provides:

- direct Stdio MCP sessions with exclusive stdin/stdout protocol ownership, executable+argv spawning, cwd/environment/secret injection, stderr-only logging/redaction, Unix process-group ownership, Windows process-tree ownership, and bounded graceful/forced teardown;
- Managed HTTP sessions where the Manager-owned process and direct MCP client are one lifetime unit and process exit is surfaced fail-closed;
- External HTTP sessions that connect/close without taking ownership of the remote service;
- secret-backed static Authorization/API headers and downstream OAuth handler integration scoped by immutable Server ID;
- existing v2 HTTPS/insecure-override validation enforced before connection;
- complete paginated `tools/list` snapshots and deterministic full-contract fingerprints;
- immediate tool-contract invalidation on advertised list-change notifications and pre-call fingerprint revalidation for servers that do not advertise reliable notifications;
- narrow Tasks extension registration on the official SDK client rather than a second protocol implementation;
- panic-contained diagnostic/log callbacks and secret redaction for child-process logs.

Checkpoint B integration coverage performs real `ListTools -> CallTool` flows over all three transports without a per-server Secure MCP Tunnel or downstream `tunnel-client`. Additional tests cover static authentication, OAuth authorize/retry, server-scoped OAuth state, environment-secret injection, log redaction, tool-contract invalidation, and owned-process teardown.

CI run `33229784822` completed successfully after correcting requested Stdio EOF teardown semantics: `go mod tidy` produced zero diff, `go test ./...` and `go vet ./...` passed, all six headless cross-build targets passed, and native Linux/Windows/macOS GUI builds passed.

Phase 3 exit gate is satisfied: arbitrary test MCPs can be listed and called safely through the Manager's direct downstream client layer with no per-server `tunnel-client`.

### Phase 4 — catalog, generations, and routing state

Next. Implement the Portable-Root-local SQLite catalog for authoritative source contracts, content fingerprints, active/staging generations, persistent staging resume, dirty partitions, dependency/neighborhood hashes, integrity validation/quarantine, and atomic generation promotion tied to the Phase 2 routing-state hash contract.

## Clean v2 break

V2 intentionally does not preserve compatibility with v1 configuration or routing data. The implementation initializes clean v2 state rather than carrying v1 compatibility structs, aliases, migration journals, or conversion logic. Existing v1 state may be moved aside as opaque discardable legacy data during the one major-version cutover, but it is not parsed or converted.
