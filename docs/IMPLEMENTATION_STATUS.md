# Implementation Status

Date: 2026-08-29

## Current released baseline

`main` was frozen for v2 planning at commit `08366ffbd299177870c10a3446ab9e4dcd35a18e` (`Release v1.0.32`). The released application still uses the v1 per-server tunnel/plugin/lifecycle architecture.

## V2 implementation branch

Branch: `feature/v2-mcp-router`

Status: **v2 implementation complete; Phases 1–13 complete and the Definition of Done is satisfied on the feature branch. Release/merge remains a separate explicit action**.

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
- non-blocking Ambiguity Reviews generated only from accepted reconciliation output, with source-grounded summaries/comparative details, conditional use cases, and suggested preference options; open reviews do not block promotion and may be resolved after promotion without semantic reindexing;
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

Complete through commit `2819fd2ac06b840b470ad32d9681b53461141543` (primary discovery/handle implementation `760dc79c91cc06108d1d0a8076e83c57bf7aab57`).

`internal/discovery`, `internal/executionhandle`, and the completed Phase 4–6 catalog/retrieval/enrichment/preference substrates now provide:

- fail-closed discovery against only the current active Index Generation and Routing State Hash, with search returning `index_required` rather than consulting stale or incomplete semantic state;
- query-time use of the configured embedding provider plus the bounded memory-only query cache, without persisting user query text or query vectors;
- independent lexical/BM25, source-description vector, input-schema vector, enriched semantic-guidance vector, and capability-centroid retrieval signals over committed catalog state;
- deterministic weighted reciprocal-rank fusion with stable tie-breaking and bounded Routing Preference adjustments rather than allowing preference overlays to replace semantic evidence;
- explicit no-match thresholding that may return zero Tool References instead of filling the requested limit with low-confidence candidates;
- Routing Profile precedence of explicit request profile over configured default profile over Global-only preferences, with an explicit missing profile returning `routing_profile_not_found` and no silent named-profile fallback;
- compact Tool References that resolve only against the active generation and a `get_tool` detail path that keeps the original authoritative downstream tool contract separate from derived semantic guidance, capability membership, human-readable identity, normalized executor class, and execution material;
- process-epoch HMAC-authenticated Execution Handles bound to generation ID, Server ID, tool name, authoritative source fingerprint, and normalized executor class, with tampering, cross-process reuse, and changed source/executor assumptions rejected;
- reusable MCP registration for the two Phase 7 discovery/detail tools (`search_tools` and `get_tool`) without pulling the Phase 8 downstream execution router or Managed lifecycle/use-lease behavior forward;
- deterministic integration fixtures built through the real catalog, persistent retrieval roles, enrichment artifacts, capability hierarchy, active-generation promotion, Routing Preferences, query cache, and handle manager rather than bypassing those boundaries with ranking-only mocks;
- an explicit GitHub Actions Phase 7 quality-gate step so the Section 18 measurements remain visible and release-blocking on every CI run.

Focused Phase 7 tests cover active-generation fail-closed behavior, zero-result no-match search, authoritative source-contract preservation, compact Tool Reference round-trips, `get_tool` semantic/capability separation, explicit/default/missing Routing Profile behavior, conditional preference precedence, handle tamper rejection, process-restart handle invalidation, source-fingerprint/executor binding, and the committed Section 18 evaluation corpus.

Phase 7 quality CI run `33264318043` completed successfully. The recorded Section 18 results were:

| Gate | Result | Required |
| --- | ---: | ---: |
| Critical must-route top-5 | 100.0% | 100% |
| General top-1 | 96.4% | >= 90% |
| General top-5 | 100.0% | >= 98% |
| No-match false-positive rate | 2.0% | <= 2% |
| Explicit Routing Preference adherence | 100.0% | 100% |
| Executor/safety mapping | 100% | 100% |

The same run passed `go mod tidy` with zero diff, `go test ./...`, the dedicated verbose Phase 7 discovery quality gate, `go vet ./...`, the Phase 5 retrieval benchmark step, native Linux/Windows/macOS GUI builds, Windows process/DPAPI checks, and all six headless `CGO_ENABLED=0` cross-build targets.

Phase 7 exit gate is satisfied: every canonical Section 18 quality/safety threshold passes before execution routing is enabled, discovery remains index-backed and downstream-independent, preference/profile semantics are deterministic, and authenticated generation/source/executor-bound execution material is available for Phase 8 without Phase 8 dispatch having started.

### Phase 8 — execution routing and permission-preserving dispatch

Complete through commit `499d2b3e6c487f527262bbbfa95f4f98a2179742` (primary router implementation `63bef3c7731e689f59e684da5e0b1f39f4438260`, executor registration `35fa615e917cf61a0fb1c88cd2ce841e325564c9`).

`internal/executionrouter` and the staged Phase 8 Manager registration now provide:

- one shared execution-router core for all ten permission classes, with explicit catalog/current-generation, routing-state, execution-handle, downstream-session, live-contract invalidation, and result-limit dependencies rather than ten independent dispatch implementations;
- fail-closed Execution Handle validation covering HMAC/process binding, active current generation, generation ID, Server ID/tool identity, authoritative source fingerprint, normalized executor class, and exact Manager executor endpoint binding before dispatch;
- caller-supplied human-readable tool identity retained only as display/logging metadata, with routing authority derived exclusively from the authenticated handle plus current authoritative catalog state;
- safe Manager-side argument validation using the authoritative downstream input schema with only in-document/local `#` and `$defs` resolution; no HTTP, filesystem, or other external `$ref` loader is provided or invoked;
- full authoritative server `tools/list` fingerprint comparison at execution time, including the case where a newly established downstream session already reflects drift, with mismatch marking the `server:<id>` routing partition dirty, advancing routing revision, returning `index_required`, and preventing dispatch;
- preservation of existing Phase 3 notification-aware/pre-call contract revalidation, translating a discovered live contract change into the same fail-closed routing invalidation path;
- an exactly-once downstream `CallTool` boundary with explicit `not_started`, `completed`, and `outcome_unknown` semantics; ambiguous post-dispatch transport failures are non-retryable and never automatically replayed, including for idempotent executor classes;
- direct pass-through of the official MCP `CallToolResult` object for successful completed calls so text, image, audio, structured content, resource links, embedded resources, and downstream `isError` survive without lossy reconstruction;
- configured result-size enforcement where an oversized completed result returns `result_too_large` with `outcome=completed` and `retryable=false`, plus post-call invalid-result handling that preserves the original result/isError context internally where possible;
- all ten staged Manager executor registrations with their exact permission-class annotations; read-only classes leave destructive/idempotent-only hints neutral, and every registration normalizes back to its own authoritative executor class;
- a Phase 7+8 staged router server exposing discovery/detail plus the ten executors without pulling Phase 9 lifecycle work forward; the canonical fixed 19-tool upstream surface remains Phase 10 work as specified by the implementation plan.

Focused Phase 8 tests cover all ten registration/class mappings, valid-handle dispatch, HMAC tampering, process restart invalidation, stale generations, source-fingerprint mismatch, executor-class mismatch, wrong executor endpoint, no-current-generation fail-closed behavior, ordinary/nested/array schema validation, local `$defs` references, malformed schema rejection, HTTP/file external `$ref` rejection, no downstream call after validation failure, initial-session and pre-call live contract drift, dirty-partition/routing-revision advancement, pure downstream availability semantics, ambiguous idempotent-call no-replay behavior, mixed/structured result fidelity, completed result-size overflow, invalid post-call result handling, and an actual direct External HTTP MCP round-trip through the Phase 3 downstream client layer.

Phase 8 implementation CI run `33266084319` completed successfully: `go mod tidy`/module-graph verification produced zero diff, `go test ./...` passed, the dedicated Phase 7 quality gate remained green, `go vet ./...` passed, the retrieval-scale benchmark step passed, native Linux/Windows/macOS GUI builds passed, Windows process/DPAPI checks passed, and all six headless `CGO_ENABLED=0` cross-build targets passed.

Phase 8 exit gate is satisfied: the safety/executor registration matrix is complete and normalizes correctly, authenticated authority and schema/live-contract failures stop before unsafe dispatch, ambiguous calls are never replayed automatically, and representative mixed MCP results round-trip through the direct downstream path without result loss. Phase 9 lifecycle work remained intentionally deferred from Phase 8.

### Phase 9 — router-native lifecycle

Complete through commit `d41c3a9d12f4d4aabe94556d76fac594bf9b1f6e` (primary lifecycle implementation `24ba54d5b515b922ac82223af0c8462ca8851df0`).

`internal/routedlifecycle`, the existing `internal/executionrouter` provider boundary, and `internal/v2state` mutation coordination now provide:

- a v2-native downstream-session/lifecycle provider satisfying the existing Phase 8 `executionrouter.SessionProvider` boundary while reusing the Phase 3 `internal/downstream.Factory` and `downstream.Session` substrate rather than reintroducing per-server Secure Tunnels or downstream `tunnel-client` ownership;
- context-aware per-server acquisition/transition serialization with only short map lookup locking and no registry-wide lifecycle mutex on routed hot paths, allowing unrelated servers to start/acquire/call concurrently while same-server start/stop/restart/acquire decisions remain serialized;
- idempotent Managed Use Leases that reserve active use before potentially slow startup, hold the exact runtime/session reference for the lease lifetime, expose accurate active-use accounting, update Manager-owned activity timestamps directly, and release on every router success/error/drift/result/cancellation path after acquisition;
- Managed stopped-to-routed-use auto-start and final-release idle-stop behavior driven exclusively by Manager-owned lease/call activity timestamps and configured v2 Manager/per-server idle timeouts, including idle-timer cancellation/reset when a new lease arrives;
- bounded task-held leases using the same lifecycle primitive, with explicit terminal release plus automatic expiry, without adding the Phase 10 Manager task mapping/proxy protocol surface early;
- correct mode behavior: stopped Manual servers return an explicit blocker and are never auto-started by routed acquisition; Disabled servers reject acquisition/start; Always On servers are maintained/reused, ordinary stop is rejected, and unexpected runtime exit triggers bounded retry/re-establishment while the Manager remains active;
- stable lifecycle blocker classification through the execution router for `manual_server_stopped`, `server_disabled`, `server_busy`, and `manager_shutting_down`, preserving Phase 8 `not_started` semantics when dispatch never began;
- routing/runtime mutation reservations at the v2 state coordinator boundary so edits that affect name/mode/transport/environment/runtime, disable, delete, stop, or restart fail immediately with `server_busy` while use leases are active, carrying the exact `active_call_count`, without queuing or forcibly interrupting active use; logging-only Server Entry changes remain non-tearing and independently mutable;
- deterministic crash/close behavior: a crashed Managed runtime is detached without corrupting outstanding lease accounting, a later acquisition reconnects it, an Always On crash is re-maintained, Manager shutdown cancels active Manager-owned routed call contexts, stops idle timers, and closes current Manager-owned downstream sessions/runtimes;
- preservation of Phase 3 notification-triggered and non-notifying pre-call tool-contract revalidation on reused sessions, with Phase 8 translating `ErrToolContractChanged` into dirty routing state, routing-revision advancement, `index_required`, and no unsafe dispatch/replay;
- no v2 routed-lifecycle dependency on the legacy `internal/servers.Registry.opMu`, `Runtime.Activity()`, `ActivityTracking()`, `tunnel_client_telemetry`, lifecycle markers, lifecycle skills, Developer Plugins, Tunnel IDs, or downstream `tunnel-client` choreography.

Focused Phase 9 tests cover unrelated-server concurrent acquisition, same-server single-start/multiple leases, Managed auto-start, active-call idle protection, final-release idle-stop, idle reset, task terminal/expiry release, Manual/Disabled/Always On semantics, `server_busy` edit/disable/delete/stop/restart protection with exact multi-lease counts, non-runtime logging mutation, failed-start accounting rollback, Managed and Always On crash handling, Manager-shutdown cancellation/owned-runtime cleanup, lease release on all router post-acquisition paths, lifecycle blocker mapping, v2-state pre-persistence mutation reservation, and live tool-contract drift through reused sessions.

Phase 9 implementation CI run `33267763059` completed successfully on `d41c3a9d12f4d4aabe94556d76fac594bf9b1f6e`: committed module-graph verification produced zero diff, `go test ./...` passed, the dedicated Phase 7 quality gate remained green, `go vet ./...` passed, the retrieval-scale benchmark step passed, native Linux/Windows/macOS GUI builds passed, Windows process/DPAPI checks passed, and all six headless `CGO_ENABLED=0` cross-build targets passed.

Phase 9 exit gate and Checkpoint E-style routed lifecycle coverage are satisfied: Managed lifecycle starts from routed use, active calls/tasks retain explicit leases, idle shutdown is driven by Manager-owned direct activity rather than tunnel telemetry, unrelated servers are not serialized behind a registry-wide hot-path mutex, runtime/routing mutations fail `server_busy` during active use, crash/cancellation/shutdown cleanup is deterministic, and live contract drift remains fail-closed. Phase 10 — final upstream Manager MCP surface and protocol proxying — is next and has not been started as part of Phase 9.

### Phase 10 — full Manager MCP upstream surface

Complete through implementation commit `11f4ced1f78c7658e16f199a5636a2851a68b805`.

`internal/mcpmanager`, `internal/indexing`, the continuation/catalog additions, and the completed Phase 3–9 downstream/routing substrates now provide:

- one canonical loopback Manager MCP endpoint at `/mcp` routing both MCP 2026-07-28 modern stateless Streamable HTTP requests and older stateful legacy sessions into the same logical router implementation;
- the fixed 19-tool Manager contract: five indexing/enrichment tools, two discovery/detail tools, two Routing Preference tools, and all ten permission-preserving executor classes with stable annotations independent of downstream tool count;
- Manager-owned indexing orchestration for `index_status`, `index_refresh`, enrichment-batch retrieval/submission, and atomic `index_commit`, reusing the existing catalog, retrieval, enrichment, lifecycle, and routing-state correctness boundaries instead of duplicating them in the protocol layer;
- optional local Bearer-capability protection backed by the Manager secret store, enabled by configuration, while browser-originated requests are rejected unconditionally whether capability protection is enabled or disabled;
- one continuation service shared by routed execution and the upstream protocol, with persistent opaque Manager task/resource mappings stored in the catalog and HMAC-authenticated Manager-owned resource references;
- downstream Task proxying that rewrites downstream task IDs into Manager-owned opaque IDs, keeps bounded Managed task-use leases alive until terminal state or expiry, supports get/update/cancel continuation operations, and can re-establish durable task mappings after Manager restart without replaying the originating tool call;
- resource-link continuation proxying that rewrites only downstream `ResourceLink` URIs, leaves embedded resources unchanged, persists the original downstream URI internally, reacquires the correct routed lifecycle session for a later read, and forwards the original resource request;
- narrow Tasks wire-response compatibility around the official Go SDK so polymorphic downstream task results can traverse the Manager endpoint without replacing the SDK transport/protocol implementation;
- legacy callback forwarding for elicitation, sampling, and roots through the routed downstream session, including serialized callback-capable legacy calls and stable rejection when the upstream client lacks a required callback capability;
- shared routed lifecycle/session acquisition for execution, task follow-up, resource follow-up, and callbacks, preserving the existing Phase 8/9 fail-closed live-contract checks, exactly-once dispatch boundary, per-server concurrency, and active-use accounting;
- JSON-valued MCP projections for enrichment-batch request/response payloads and authoritative tool contracts so generated Manager output schemas match the actual object-shaped wire data rather than exposing `json.RawMessage` as byte arrays;
- canonical capability-reconciliation artifact identity based on the exact stored canonical batch request representation, eliminating prepare/submit key-order dependence while retaining immutable-batch and corruption checks;
- serialization-stable downstream tool snapshots whose fingerprints remain self-consistent after cloning/persistence round trips, preventing false live-contract invalidation while preserving real drift detection.

Focused Phase 10 integration coverage uses a real modern MCP client and raw legacy protocol requests against the canonical endpoint. It verifies the stable fixed tool surface/annotations, complete `index_refresh -> enrichment batch -> submit -> index_commit -> search_tools -> get_tool -> executor` workflow, authoritative-contract object projection, routed downstream execution, Manager-owned resource-link rewriting and follow-up `resources/read`, local capability enforcement, unconditional browser-Origin rejection, and modern/legacy dispatch behavior. Existing focused continuation/callback/downstream suites retain the task, legacy callback, cancellation, result-fidelity, and lifecycle guarantees inherited by the canonical server.

Phase 10 implementation CI run `33272556036` completed successfully on `11f4ced1f78c7658e16f199a5636a2851a68b805`: committed module-graph verification produced zero diff, `go test ./...` passed including the new canonical upstream integration workflow, the dedicated Phase 7 discovery quality gate remained green, `go vet ./...` passed, the retrieval-scale benchmark step passed, native Linux/Windows/macOS GUI builds passed, Windows process/DPAPI checks passed, and all six headless `CGO_ENABLED=0` cross-build targets passed.

Phase 10 exit gate is satisfied at the implementation level: representative modern and legacy upstream clients see the stable 19-tool Manager contract and complete tool workflows, protocol-required task/resource/callback continuations remain routed through Manager-owned mappings and lifecycle leases, local access protection/Origin rejection is enforced at the one-URL boundary, and the integration client exercises the real catalog/index/discovery/execution path rather than registration-only mocks. Phase 11 native UI/bootstrap migration has not been started as part of Phase 10.

### Phase 11 — native desktop migration

Complete through implementation commit `a15f84a0cf63f6ca38e50a4d581d1b2ea2a9c44c`.

The executable bootstrap, `internal/app.V2App` facade, native Gio desktop modules, and v2 product-runtime services now provide:

- canonical v2 startup for both desktop and headless execution, including opaque legacy cutover, direct construction of the Phase 3–10 catalog/routing/lifecycle/indexing/Manager MCP stack, and no runtime fallback through the v1 `App` bootstrap;
- one native v2 desktop surface for Server Entries using immutable generated `srv_<32 hex>` IDs, all four lifecycle modes (`Always On`, `Managed`, `Manual`, `Disabled`), and all three downstream transports (Stdio, Managed HTTP, External HTTP);
- user-facing HTTP authentication workflows for none/OAuth/static authentication, explicit OAuth connect/reconnect/disconnect behavior, static-header credentials and environment secrets written through generated secret references, and no requirement for users to type or manage `secret://` references directly;
- explicit server start/stop/restart controls and live running/active-call state backed by the router-native Phase 9 lifecycle service rather than per-server tunnel or lifecycle-skill controls;
- embedding-provider configuration including base URL, model, optional dimensions, and credential entry, plus query-embedding cache and Managed default-idle configuration through the validated v2 Manager schema;
- explicit Index status, refresh, and commit controls with visible pending-required/open-review blockers, agent-enrichment batch visibility, and backend-defined structured Ambiguity Review resolution using the Phase 6 request/response contract;
- Routing Profile creation/deletion/default selection and Routing Preference creation/confirmation/deletion using authoritative tool targets and current assumption fingerprints, all through optimistic `preference_revision` semantics with `needs_review` surfaced natively;
- Local Manager port and capability-protection settings, with the single optional Manager Secure MCP Tunnel configured separately using its canonical runtime credential reference and no downstream Tunnel IDs, OpenAI API keys, Developer Plugin setup, marker, or lifecycle-skill UX;
- a v2 product-runtime adapter preserving structured log capture/filter/clear/export, launch-at-startup application, tray/start-minimized/close-to-tray/confirm-exit behavior, tunnel-client install/update/rollback support for the Manager Tunnel only, and the existing staged application self-update flow;
- complete editable v2 Manager settings for native behavior, logging capture/display/disk rotation, tunnel-client channel/binary/update cadence, appearance theme, local Manager, embedding/index/Managed defaults, and Manager Tunnel state without JSON editing;
- focused facade coverage for generated valid Server IDs, all three transport configuration shapes, OAuth/static/environment-secret indirection, full Manager-schema persistence, and secret-redacted logging/export behavior.

Phase 11 first exit-gate CI run `33276157337` completed successfully on `a15f84a0cf63f6ca38e50a4d581d1b2ea2a9c44c`: committed module-graph verification produced zero diff, `go test ./...` passed including the Phase 11 facade coverage, the dedicated Phase 7 discovery quality gate remained green, `go vet ./...` passed, the retrieval-scale benchmark step passed, native Linux/Windows/macOS GUI builds passed, Windows process/DPAPI checks passed, and all six headless `CGO_ENABLED=0` cross-build targets passed.

The Phase 11 status-documentation head `b0171f471f912dbbcd028813b422e904bb3ccf0f` was then verified by a second fully green CI run `33276700912` before Phase 12 work began.

Phase 11 exit-gate implementation requirements are satisfied: a fresh v2 user can manage the v2 Manager configuration, downstream Server Entries/authentication, embedding/index workflows, Ambiguity Reviews, and Routing Profiles/Preferences through native UI without manually editing JSON or secret references; the desktop/headless executable paths start the v2 bootstrap directly; and native tray/logging/update behavior remains available without reintroducing per-server tunnel/plugin/marker topology.

### Phase 12 — remove old topology

Complete through implementation commit `adbfde16f84c6ee29b2d6abc29df0f3aa4e5e6f3` (primary topology-removal commit `b855a47060144d4033828828648521aaf6b324d5`).

The active v2 tree now removes the superseded topology rather than merely leaving it unreachable:

- deleted `assets/lifecycle-skill/`, `internal/lifecycleskill/`, and `internal/marker/` completely;
- deleted the legacy `internal/servers` registry/runtime/supervisor/crash-guard implementation and its tests, eliminating the old per-server tunnel/lifecycle ownership path;
- deleted the old v1 `internal/app.App` facade/public surface and the obsolete `internal/events` bus that existed for that runtime;
- deleted the complete legacy `internal/config` package after detaching logging from its schema; v2 configuration remains exclusively under `internal/v2config` plus the v2 state coordinator;
- deleted the old four-tool lifecycle `internal/mcpmanager/server.go` implementation while retaining the canonical Phase 10 `V2Server`; the shared structured tool-error envelope now lives in a v2-neutral file;
- removed obsolete staged `NewDiscoveryServer` / `NewRouterServer` constructors that depended on the deleted legacy Manager server, while keeping the reusable v2 discovery/execution registration functions used by the canonical 19-tool Manager;
- deleted the unreachable v1 desktop server/settings/logging/manager-control/self-update modules after Phase 11 moved all executable startup and native UX to the v2 surface; only neutral shared styling/tray helpers and `desktop_v2_*` modules remain active;
- retained `internal/tunnelclient` only for the optional Manager Secure MCP Tunnel installer/update/runtime path, narrowed its runtime contract to one Manager MCP URL plus Manager tunnel ID/credential/health/logging, and removed Stdio-command targeting, downstream activity telemetry, `MCPCommand`, `Activity()`, and the Stdio handshake test;
- retained `internal/lifecycle` only as a generic bounded jittered `Backoff` used by the optional Manager tunnel, removing the superseded Desired/Observed/Phase server/tunnel state enums;
- preserved direct Stdio/Managed HTTP/External HTTP downstream clients, router-native Managed Use Leases, Manager-owned activity timestamps, live tool-contract invalidation, and the canonical 19-tool Manager surface with no dependency on the removed topology.

A recursive feature-branch tree audit after the removal found no active `internal/servers`, `internal/lifecycleskill`, `internal/marker`, or `internal/config` paths. The Phase 11-to-Phase 12 compare shows the removals are confined to the superseded v1 topology and unreachable desktop implementation, while the only retained tunnel-client runtime is the Manager-URL path described above. Historical ADRs remain as architecture history and are not active runtime dependencies.

Phase 12 implementation CI run `33277299441` completed successfully on `adbfde16f84c6ee29b2d6abc29df0f3aa4e5e6f3`: committed module-graph verification produced zero diff, `go test ./...` passed, the dedicated Phase 7 discovery quality gate remained green, `go vet ./...` passed, the retrieval-scale benchmark step passed, native Linux/Windows/macOS GUI builds passed, Windows process/DPAPI checks passed, and all six headless `CGO_ENABLED=0` cross-build targets passed.

The Phase 12 status-documentation head `a9d9b64d6465425be95ace26e8c7b2b479e83623` was then verified by a second fully green CI run `33277433491` before Phase 13 hardening began.

Phase 12 exit gate is satisfied: active downstream routing/lifecycle code has no dependency on per-server Secure Tunnels, lifecycle markers, lifecycle skills, the old registry/runtime topology, or v1 configuration; `internal/tunnelclient` remains solely for the one optional Manager Secure MCP Tunnel.

### Phase 13 — full verification and release hardening

Complete through implementation commit `9d4c60f11ce87ee4f60b667eb3b0ebef8584eb82`.

Phase 13 closes the remaining verification, secure-exposure, release, and clean-break gaps while reusing the substantive protocol/index/routing/lifecycle suites established in Phases 1–12:

- fixed the optional Manager Secure MCP Tunnel to interoperate with default-enabled Local Manager Access Protection: the Manager capability is loaded from the secret store, registered with the central redactor, and supplied to `tunnel-client` as an environment-backed MCP `Authorization` header; the capability value never appears in argv or persisted configuration, and CR/LF injection is rejected at the tunnel-client runtime boundary;
- made Manager Tunnel reconfiguration follow the actual local Manager boundary by restarting the tunnel when the loopback Manager port or Local Manager Access Protection setting changes, in addition to existing tunnel ID/runtime-client changes;
- hardened application self-update for the v1-to-v2 clean break across both ZIP and tar.gz release formats: `config/`, `data/`, and `tools/` remain protected user-data roots, v2 archives containing the obsolete top-level `lifecycle-skill/` package are rejected, and generated Windows/Unix updater scripts explicitly remove that old packaged directory from an existing installation before copying staged v2 application files;
- added focused self-update tests for ZIP and tar.gz executable staging/renaming, protected data paths, path traversal rejection, obsolete lifecycle-bundle rejection, and Windows/Unix updater-script clean-break policy;
- rewrote the public README around the v2 fixed-Manager/direct-downstream architecture, authentication boundaries, indexing/routing workflow, single optional Manager Tunnel, six-target verification, and clean self-update behavior, removing the old per-server tunnel/plugin/marker/lifecycle-skill setup instructions;
- hardened the release workflow around v2: it now has a verification job before packaging, runs module-graph/test/search-quality/self-update-clean-break/vet gates, packages no lifecycle-skill bundle, fails if an obsolete bundle reaches staging, and generates v2-native release highlights instead of the deleted four-tool/per-server-tunnel architecture;
- promoted `go test -race ./...` to a required CI gate for the whole repository;
- expanded native desktop verification from three host-default builds to six explicit native release targets on matching runners: Linux amd64, Linux arm64, Windows amd64, Windows arm64, macOS amd64, and macOS arm64;
- retained the six `CGO_ENABLED=0` headless cross-build targets as an independent portability gate;
- retained the committed Section 18 search-quality gate and exact-cosine/BM25 retrieval-scale benchmarks in the final matrix.

The earlier focused and integration suites remain the verification basis for the rest of the Phase 13 checklist: real Stdio/Managed HTTP/External HTTP downstream MCP flows and OAuth/static credential boundaries from Phase 3; crash/restart/corruption and atomic-generation behavior from Phase 4–6; preference conflict/review semantics from Phase 6–7; modern/legacy Manager protocol, task/resource/callback continuation, local capability/Origin enforcement, and end-to-end index-to-executor workflow from Phase 10; and router-native lifecycle/failure/cancellation coverage from Phases 8–9. Phase 13 did not replace these mature tests with duplicate shallow fixtures.

Phase 13 hardening CI run `33278481021` completed successfully on `9d4c60f11ce87ee4f60b667eb3b0ebef8584eb82`. The committed module graph produced zero diff; `go test ./...`, `go test -race ./...`, the dedicated Phase 7 search-quality gate, `go vet ./...`, and the retrieval-scale benchmarks all passed. Native builds passed on Linux amd64/arm64, Windows amd64/arm64, and macOS amd64/arm64; both Windows native targets also passed the no-console child-process and DPAPI checks. All six headless `CGO_ENABLED=0` cross-build targets passed.

Phase 13 exit gate and the v2 Definition of Done are satisfied on `feature/v2-mcp-router`: no unresolved compatibility blocker remains in the accepted v2 contract, the optional Manager Tunnel now respects default local capability protection without exposing the capability through argv, the release/self-update path enforces the v2 clean break while preserving user data, the full repository is race-clean under CI, and all six native release architectures build successfully. No release, tag, merge, or change to `main` is part of this completion checkpoint.

## Clean v2 break

V2 intentionally does not preserve compatibility with v1 configuration or routing data. The implementation initializes clean v2 state rather than carrying v1 compatibility structs, aliases, migration journals, or conversion logic. Existing v1 state may be moved aside as opaque discardable legacy data during the one major-version cutover, but it is not parsed or converted.