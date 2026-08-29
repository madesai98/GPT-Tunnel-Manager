# GPT Tunnel Manager v2 Implementation Status

Date: 2026-08-29  
Release line: **v2.0.0**  
Implementation status: **Complete — Phases 0–13 and the v2 Definition of Done are satisfied**

The canonical product/architecture contract is `docs/V2_IMPLEMENTATION_PLAN.md`. `CONTEXT.md` and ADR 0009 onward define the accepted v2 terminology and architectural decisions.

The historical pre-v2 baseline was `main` commit `08366ffbd299177870c10a3446ab9e4dcd35a18e` (`Release v1.0.32`). V2 was implemented on `feature/v2-mcp-router` as a clean architecture/configuration break from that baseline.

## Release readiness

The substantive Phase 13 hardening checkpoint is commit `9d4c60f11ce87ee4f60b667eb3b0ebef8584eb82`. CI run `33278481021` completed successfully with:

- committed module graph verification (`go mod tidy` produced zero diff);
- `go test ./...`;
- `go test -race ./...`;
- the dedicated search-quality gate;
- `go vet ./...`;
- retrieval-scale benchmarks;
- native Linux amd64/arm64 builds;
- native Windows amd64/arm64 builds plus no-console child-process and DPAPI checks;
- native macOS amd64/arm64 builds;
- all six independent `CGO_ENABLED=0` headless cross-build targets.

The Phase 13 completion/status checkpoint `34c4ea28df0de5b65dfde819e8dc902565574cb3` also passed CI in run `33278757543`.

Release metadata and public release notes are configured for `v2.0.0`. The `Release` workflow re-verifies the repository before packaging and publishing six native binaries, source archives, checksums, and changelog artifacts. The `Release Page` workflow applies the curated v2 release notes after a successful release.

## Implemented v2 architecture

V2 provides:

- one fixed 19-tool Manager MCP;
- direct downstream MCP clients for Stdio, Managed HTTP, and External HTTP;
- one optional Manager Secure MCP Tunnel only;
- no per-server Secure MCP Tunnels, Developer Plugins, lifecycle markers, or lifecycle skills;
- strict v2-native configuration with no v1-to-v2 parsing/conversion path;
- four lifecycle modes: Always On, Managed, Manual, and Disabled;
- per-server lifecycle synchronization with router-native Managed Use Leases;
- Managed automatic start, active-call/task retention, and idle-stop;
- accurate `server_busy` mutation protection with `active_call_count`;
- manager-owned activity timestamps rather than tunnel telemetry;
- a persistent generation-based SQLite Tool Catalog and semantic index;
- deterministic Routing State Hash freshness plus atomic generation promotion;
- content-addressed lexical/vector/enrichment artifacts and incremental invalidation;
- semantic-neighborhood membership invalidation;
- agent-driven tool enrichment and capability reconciliation;
- non-blocking Ambiguity Reviews;
- persistent Global/profile-scoped Routing Preferences with independent preference revision and conflict/review semantics;
- multi-signal search with deterministic reciprocal-rank fusion and no-match thresholding;
- compact Tool References and authoritative `get_tool` detail;
- HMAC-authenticated generation/source/class-bound Execution Handles;
- ten ToolAnnotation-derived permission-preserving execution classes;
- safe local-only JSON Schema reference handling with no arbitrary remote/file `$ref` resolution;
- exact downstream result fidelity and outcome-aware non-replay error semantics;
- live downstream tool-contract drift detection that fails closed and marks routing stale;
- modern MCP 2026-07-28 stateless Manager support plus legacy stateful compatibility where callbacks require it;
- Manager-owned Tasks/resource continuation mappings, MRTR bridging, cancellation, and legacy callback compatibility;
- downstream OAuth/static authentication as a separate credential boundary;
- Local Manager capability protection enabled by default and unconditional browser-Origin rejection;
- a native Gio desktop application for all normal v2 configuration and operational workflows;
- staged SHA-256-verified application self-update with protected user-data roots and explicit removal/rejection of the obsolete packaged lifecycle-skill bundle.

## Phase completion

### Phase 0 — baseline and planning contract

Complete. The v1.0.32 baseline was frozen and the v2 architecture/ADR contract established before production migration work.

### Phase 1 — MCP compatibility spike

Complete. Executable compatibility coverage proved direct downstream transports, modern/legacy upstream negotiation, pagination/list-change behavior, structured results, cancellation, MRTR/input-required flows, legacy callbacks, Tasks extension handling, resource followups, and downstream OAuth viability.

### Phase 2 — clean v2 config/state foundation

Complete. Strict v2 schemas, atomic initialization, stable local Manager port, secret indirection, Routing State Hash/revisions, and fail-closed config/secret mutation coordination were implemented without v1 conversion code.

### Phase 3 — direct downstream MCP clients

Complete. Dedicated Stdio, Managed HTTP, and External HTTP clients now own the correct process/session boundaries, authentication, redaction, teardown, complete tool snapshots, and drift invalidation.

### Phase 4 — catalog, generations, and routing state

Complete. The pure-Go SQLite catalog provides authoritative contracts, routing-state persistence, staging/active generations, dependency/invalidation metadata, corruption quarantine, reusable content-addressed artifacts, and atomic validated promotion.

### Phase 5 — embedding and deterministic retrieval substrate

Complete. OpenAI-compatible embeddings, memory-only query cache, lexical/BM25 retrieval, exact cosine vectors, deterministic projections, and persistent content-addressed retrieval artifacts were implemented and benchmarked at 1k/5k/10k tools.

### Phase 6 — semantic enrichment, reconciliation, and preferences

Complete. Immutable multi-agent enrichment batches, semantic-neighborhood invalidation, enriched embeddings, capability reconciliation, non-blocking Ambiguity Reviews, Routing Profiles/Preferences, optimistic preference revisions, and review/conflict semantics are implemented.

### Phase 7 — search quality and discovery semantics

Complete. Multi-facet retrieval, weighted reciprocal-rank fusion, no-match thresholding, profile/preference overlays, Tool References, authoritative detail, and authenticated Execution Handle minting are implemented. The committed search-quality release thresholds pass.

### Phase 8 — execution routing

Complete. All ten execution classes use one fail-closed router core with handle/source/class validation, safe schema validation, exact downstream dispatch, result fidelity, size bounds, live-contract invalidation, and outcome-aware non-replay errors.

### Phase 9 — router-native lifecycle

Complete. Per-server acquisition synchronization, Managed Use Leases, automatic Managed start/idle-stop, task-held leases, Always On/Manual/Disabled semantics, server-busy mutation protection, crash/shutdown cleanup, and direct Manager activity tracking are implemented without the old lifecycle topology.

### Phase 10 — full Manager MCP surface

Complete. One protocol-aware `/mcp` endpoint exposes the fixed 19-tool contract with indexing/discovery/preferences/execution, local capability protection, browser-Origin rejection, task/resource continuation ownership, and required modern/legacy callback behavior.

### Phase 11 — native desktop migration

Complete. The executable bootstrap and Gio UI are v2-native. Normal users can manage Server Entries, auth, embeddings, indexing, reviews, Routing Profiles/Preferences, Manager settings/tunnel, logs, tray behavior, tunnel-client management, and self-update without editing JSON or secret-reference names.

### Phase 12 — remove old topology

Complete. The legacy server registry/runtime, lifecycle skill/marker packages, v1 app/config surface, old four-tool lifecycle Manager, and unreachable v1 desktop modules were removed. `internal/tunnelclient` remains only for the optional Manager Secure MCP Tunnel.

### Phase 13 — full verification and release hardening

Complete. Manager Tunnel/local capability integration was hardened, self-update clean-break behavior was verified for ZIP and tar.gz, README/release packaging were converted to v2, race testing became a required CI gate, and all six native release architectures plus all six headless cross-build targets are green.

## Search-quality gate

The committed evaluation gate records:

| Gate | Result | Required |
| --- | ---: | ---: |
| Critical must-route top-5 | 100.0% | 100% |
| General top-1 | 96.4% | >= 90% |
| General top-5 | 100.0% | >= 98% |
| No-match false-positive rate | 2.0% | <= 2% |
| Explicit Routing Preference adherence | 100.0% | 100% |
| Executor/safety mapping | 100% | 100% |

## Clean v2 break

V2 intentionally does not preserve compatibility with v1 configuration or routing data. Existing legacy state is treated as opaque cutover data rather than parsed or converted. `config/`, `data/`, and `tools/` remain protected Portable Root user-data directories during application replacement; the v2 runtime then initializes/uses strict v2-native state according to the clean-break policy.

The obsolete packaged `lifecycle-skill/` directory is not part of v2 releases and is explicitly removed from an existing installation during update.

## Definition of Done

The v2 Definition of Done is satisfied: a fresh v2 user can configure and route downstream MCPs, authenticate, build/commit the semantic index, resolve or defer ambiguity, apply routing preferences, discover exact tools, execute through the correct permission class, use Managed lifecycle and protocol continuations, optionally expose the single Manager MCP remotely, and manage the product through the native UI without any per-server tunnel/plugin/lifecycle choreography.
