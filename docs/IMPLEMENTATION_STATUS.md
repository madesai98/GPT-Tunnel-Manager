# Implementation Status

Date: 2026-08-29

## Current released baseline

`main` was frozen for v2 planning at commit `08366ffbd299177870c10a3446ab9e4dcd35a18e` (`Release v1.0.32`). The released application still uses the v1 per-server tunnel/plugin/lifecycle architecture.

## V2 implementation branch

Branch: `feature/v2-mcp-router`

Status: **implementation in progress; Phases 1–6 complete, Phase 7 next**.

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

Complete through commit `6718b94c034127687de816fd7df0ce26656c423c` (primary catalog implementation `38020e6c16facbc2804f6eea12e307b0d4692ae6`, dependency tidy `4dd52a1a9ce7ef0477a9d343cd9340c9aeb40e62`).

`internal/catalog` and the supporting routing/tool-contract packages now provide:

- a Portable-Root-local `data/catalog.sqlite3` database using the pure-Go `modernc.org/sqlite` driver, with foreign keys, WAL, full synchronous durability, bounded busy waiting, schema versioning, and a tested v1-to-v2 catalog schema migration;
- startup integrity checking with corrupt-database quarantine (including WAL/SHM sidecars) followed by clean initialization rather than salvage or trust of partial routing data;
- a production SQLite `routingstate.Backend` persisting `routing_revision`, `routing_state_hash`, and independent `preference_revision` state;
- a runtime routing-revision advance primitive that does not misuse revision as the correctness proof;
- persistent staging/active/superseded generation state, with matching staging generations resumable after reopen and stale staging generations superseded when the Routing State Hash changes;
- one shared deterministic MCP tool-contract fingerprint implementation used by both downstream session snapshots and catalog authoritative tool storage;
- authoritative generation-scoped source server context and full downstream tool contracts, with Server ID/tool invocation identity and content fingerprints kept separate from later semantic artifacts;
- source-server projection that intentionally excludes endpoints, environment values, credential references, runtime/logging configuration, and raw authentication material from catalog source context;
- dirty routing partitions for incremental rebuild invalidation without weakening the global Routing State Hash freshness contract;
- generation dependency records and deterministic neighborhood/context hash storage for later embedding/enrichment invalidation;
- content-addressed generic artifacts with deterministic content/dependency/context identities and validated reuse across generations;
- storage boundaries for later lexical records, capability/enrichment artifacts, Routing Profiles/Preferences, and task/resource continuation mappings without implementing later-phase pipelines prematurely;
- transactional promotion that verifies staging status, current Routing State Hash, source membership/completeness, tool source fingerprints/content integrity, dirty partitions, required dependencies/artifacts, and catalog integrity before atomically replacing the active generation;
- active-generation validation that keeps a valid active generation usable during a same-routing-state replacement build and fails closed when routing state, dirty metadata, or authoritative source integrity no longer matches.

Focused Phase 4 tests cover fresh schema creation, reopen persistence, SQLite routing-state persistence, crash/reopen staging resume, stale hash/dirty/incomplete/fingerprint/dependency promotion blockers, atomic activation and rollback behavior, content-addressed reuse, corrupt catalog quarantine/no-trust behavior, independent preference revision, staging reconciliation, supported schema migration, and server-contract credential/operational-data exclusion.

Phase 4 CI run `33231527007` completed successfully: `go mod tidy` produced zero diff, `go test ./...` and `go vet ./...` passed, all six headless cross-build targets passed, and native Linux/Windows/macOS GUI builds passed.

Phase 4 exit gate is satisfied: the authoritative catalog rebuild foundation is incremental and crash-safe, with persistent staging, dirty/dependency metadata, validated reusable artifacts, fail-closed freshness/integrity checks, and atomic activation in place.

### Phase 5 — embedding and deterministic retrieval substrate

Complete at implementation commit `2a4ad0cef2e5a94ecd2e5253c894d66a9f088759`.

`internal/embedding`, `internal/retrieval`, and the Phase 4 catalog integration now provide:

- a provider-neutral embedding interface with stable provider/model/base-URL/dimension/protocol identity and fingerprinting;
- an OpenAI-compatible `/embeddings` provider using the dedicated embedding secret reference, bounded batching, optional configured dimensions, response-order reconstruction, vector validation, context cancellation, response-size limits, and error handling that does not expose credential values or provider response bodies;
- a bounded concurrency-safe memory-only query embedding LRU keyed by exact query digest plus provider identity, with configured capacity from `manager.Index.QueryEmbeddingCacheEntries` and no query text/vector persistence;
- deterministic authoritative tool projections for source name/title/description text, canonical input-schema JSON, and the lexical source channel, each carrying an explicit projection version/fingerprint and never dereferencing remote schema references;
- persistent content-addressed embedding artifacts whose dependency identity includes exact projected input, embedding provider/model/config identity, and projection version, allowing safe cross-generation reuse while preventing reuse across model/config changes;
- explicit generation artifact requirements layered onto the existing fail-closed generation dependency/attachment model so a staging generation cannot promote when a required embedding or lexical artifact remains unfulfilled;
- generation-bound lexical records plus a portable pure-Go deterministic BM25 substrate, avoiding a required SQLite FTS/native-extension dependency and preserving the existing `CGO_ENABLED=0` release path;
- exact cosine retrieval with finite/nonzero/dimension validation, pre-normalized catalog vectors, deterministic key tie-breaking, and no ANN dependency;
- deterministic binary vector artifact encoding with dimension and payload validation on load;
- independent vector roles for source-description and input-schema channels so later Phase 7 retrieval can fuse separate ranking signals without Phase 5 hardcoding final search ranking.

Focused Phase 5 tests cover provider batching/dimensions/response ordering, malformed embedding responses, credential/error-body non-leakage, cancellation, query-cache hits/eviction/provider separation/concurrency/restart loss, vector invalid-input handling and stable ties, lexical BM25 correctness and stable ties, canonical source/schema projections and remote `$ref` non-dereferencing, content-addressed provider-bound embedding reuse, generation promotion blocking on missing required retrieval artifacts, and catalog-backed vector/lexical index loading.

Phase 5 implementation CI run `33234111003` completed successfully: `go mod tidy` produced zero diff, `go test ./...` and `go vet ./...` passed, native Linux/Windows/macOS GUI builds passed, and all six headless `CGO_ENABLED=0` cross-build targets passed.

The same GitHub Actions test job recorded the required deterministic scale benchmarks on Ubuntu 24.04 / linux-amd64, Go 1.25.14, Intel Xeon 6973P-C, using 256-dimensional exact-cosine vectors:

| Tools | Exact cosine | Exact allocs | Lexical BM25 | Lexical allocs |
| ---: | ---: | ---: | ---: | ---: |
| 1,000 | 270,510 ns/op | 25,702 B/op, 5 allocs/op | 164,569 ns/op | 24,872 B/op, 12 allocs/op |
| 5,000 | 1,640,202 ns/op | 124,000 B/op, 5 allocs/op | 818,656 ns/op | 123,176 B/op, 12 allocs/op |
| 10,000 | 3,404,594 ns/op | 246,881 B/op, 5 allocs/op | 1,584,787 ns/op | 246,056 B/op, 12 allocs/op |

Phase 5 exit gate is satisfied: deterministic vector/lexical retrieval correctness tests pass, persistent derived artifacts participate in fail-closed generation readiness, user query embeddings remain memory-only, the pure-Go portability contract is preserved, and measured 1k/5k/10k performance supports keeping exact search for the initial catalog scale rather than introducing ANN prematurely.

### Phase 6 — semantic enrichment, neighborhood invalidation, and routing preference data

Complete through commit `9311d4bf0d5a5acbf517aeac8ba11957338179ed` (primary implementation `ed26457b48a8055488d743de63111e7eea9f1933`).

`internal/enrichment`, `internal/routingprefs`, and the Phase 4/5 catalog/retrieval substrate now provide:

- a schema-v3 persistent enrichment-batch store with deterministic immutable batch identities and bounded `tool_enrichment`, `capability_reconciliation`, and optional `ambiguity_review` work items;
- multi-agent submission semantics where the first valid response wins, an identical repeat is idempotent, and a conflicting repeat returns a stable enrichment-batch conflict instead of overwriting accepted work;
- persistence of the accepted response before downstream materialization, plus restart/re-entry repair that replays the accepted response idempotently when artifact/embedding completion was interrupted after acceptance;
- bounded deterministic tool-enrichment batches built from authoritative source contracts and the Phase 5 exact-cosine source-description index;
- deterministic top-K semantic neighborhoods with ordered membership/source fingerprints included in the neighborhood context identity, so a new or changed tool entering another tool's neighborhood invalidates otherwise reusable enrichment even when no old reverse dependency existed;
- content-addressed tool-enrichment artifacts whose dependencies include the authoritative tool, semantic neighbors, protocol version, and neighborhood context, with exact dependency/context reuse only;
- a required enriched-embedding channel for accepted semantic guidance using the configured Phase 5 embedding provider/model identity, participating in generation dependency readiness;
- a required global capability-reconciliation batch and validated capability-hierarchy artifact, including known-tool membership, parent existence, acyclic hierarchy validation, and coverage that assigns every indexed tool to reconciled capability data;
- non-blocking Ambiguity Reviews generated only from accepted reconciliation output, with source-grounded summaries/comparative details, conditional use cases, and suggested preference options; open reviews do not block generation promotion and may be resolved after promotion without semantic reindexing;
- ambiguity validation that requires grounded comparative information for each competing tool without forcing invented symmetric pros/cons when the source only supports one side;
- the accepted ten ToolAnnotation-derived executor classes with MCP annotation defaults normalized independently from semantic enrichment and routing preferences;
- semantic-source plus normalized-executor preference-assumption fingerprints so preference state is bound to the authoritative semantic/execution assumptions it was written against;
- a persistent Routing Profile/Preference service with optimistic `expected_preference_revision`, identical-write no-ops, stable `preference_conflict` on stale writes, and revision changes isolated from `routing_state_hash`/semantic generations;
- deterministic preference precedence support for profile scope over Global and conditional-tool over tool-set over server specificity;
- equal-scope/equal-specificity conflict handling that marks conflicting rules `needs_review` rather than applying newest-wins behavior;
- target reconciliation that marks preferences `needs_review` when a bound tool is removed or its relevant semantic-source/executor assumption fingerprint changes, without transferring preference state to semantically similar replacements;
- an idempotent schema-v3 migration for the new batch/index state while retaining the pure-Go SQLite and `CGO_ENABLED=0` portability contract.

Focused Phase 6 tests cover immutable/unclaimed batch delivery, first-valid/idempotent/conflicting multi-agent submissions, required-generation promotion blocking, accepted-response repair both in-process and after catalog reopen, deterministic semantic-neighborhood membership invalidation, enrichment artifact reuse boundaries, enriched-embedding materialization, capability reconciliation and hierarchy validation, non-blocking post-promotion Ambiguity Reviews, source-grounded ambiguity validation, all ten executor classes/defaults, optimistic preference revisions/idempotent writes, profile/global and specificity precedence, equal-specificity review conflicts, assumption-fingerprint behavior, and removed/changed/reclassified target `needs_review` transitions.

Phase 6 implementation CI run `33261211514` completed successfully: `go mod tidy` produced zero diff, `go test ./...` and `go vet ./...` passed, the Phase 5 retrieval benchmark step remained green, native Linux/Windows/macOS GUI builds passed, Windows process/DPAPI checks passed, and all six headless `CGO_ENABLED=0` cross-build targets passed.

Phase 6 exit gate is satisfied: required tool enrichment and global capability reconciliation can complete incrementally and survive interruption, semantic-neighborhood membership changes invalidate affected enrichment deterministically, open Ambiguity Reviews do not block promotion and can be resolved later, and Routing Profiles/Preferences can be saved, changed, conflicted, and reconciled through an independent preference revision without forcing semantic reindexing.

### Phase 7 — search quality and discovery semantics

Next. Implement the accepted search-quality/fusion behavior on top of the completed deterministic retrieval, semantic-enrichment, capability, and routing-preference substrates. Do not pull later execution-handle/lifecycle work forward unless required by the canonical Phase 7 contract.

## Clean v2 break

V2 intentionally does not preserve compatibility with v1 configuration or routing data. The implementation initializes clean v2 state rather than carrying v1 compatibility structs, aliases, migration journals, or conversion logic. Existing v1 state may be moved aside as opaque discardable legacy data during the one major-version cutover, but it is not parsed or converted.
