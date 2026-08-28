# GPT Tunnel Manager v2 — MCP Compression Router Implementation Plan

Status: **Planning complete / implementation not yet started**  
Target release: **v2.0.0**  
Planning branch: `feature/v2-mcp-router`  
Baseline `main` at planning start: `08366ffbd299177870c10a3446ab9e4dcd35a18e` (`Release v1.0.32`)

This document is the canonical implementation contract for the v2 router work. It replaces the assumptions in the earlier uploaded migration draft where they conflict with accepted ADRs and planning decisions.

## 1. Product boundary

GPT Tunnel Manager v2 changes the product from a per-server tunnel/plugin lifecycle manager into a single MCP aggregation, compression, discovery, preference, and routing gateway.

GPT Tunnel Manager acts as:

1. one upstream **Manager MCP** server; and
2. a direct downstream MCP client for configured MCP servers.

Per-server Secure MCP Tunnels, per-server ChatGPT Developer Plugins, lifecycle markers, the lifecycle skill, and tunnel-client activity telemetry are removed from downstream routing. `tunnel-client` remains only for the optional Manager Secure MCP Tunnel.

V2 is a clean product/configuration break. It does not convert or interpret v1 configuration or routing data.

## 2. Fixed Manager MCP contract

The Manager MCP exposes a fixed **19-tool** surface regardless of downstream tool count:

- 5 indexing/enrichment tools;
- 2 discovery/detail tools;
- 2 routing-preference tools;
- 10 permission-preserving execution tools.

### 2.1 Indexing/enrichment tools

- `index_status`
- `index_refresh`
- `index_get_enrichment_batch`
- `index_submit_enrichment_batch`
- `index_commit`

`index_get_enrichment_batch` supports three batch kinds:

- `tool_enrichment`;
- `capability_reconciliation`;
- `ambiguity_review`.

`tool_enrichment` and `capability_reconciliation` are required work. `ambiguity_review` is optional and non-blocking.

`index_commit` requires zero pending required batches, but it may commit with open Ambiguity Reviews. `index_status` reports required work and open reviews separately.

### 2.2 Discovery/detail tools

- `search_tools`
- `get_tool`

Normal model flow is:

```text
search_tools
    -> get_tool
    -> one permission-class executor
```

`search_tools` accepts an optional explicit Routing Profile/context selector. Profile precedence is:

```text
explicit request profile
    > native default Routing Profile
    > Global preferences only
```

A requested nonexistent profile returns `routing_profile_not_found`; no silent fallback to another named profile is allowed.

### 2.3 Routing-preference tools

- `get_routing_preferences`
- `set_routing_preferences`

Annotations:

```text
get_routing_preferences:
  readOnlyHint: true
  openWorldHint: false

set_routing_preferences:
  readOnlyHint: false
  destructiveHint: true
  idempotentHint: true
  openWorldHint: false
```

Preference writes use canonical preference identities and optimistic `expected_preference_revision`. Repeating an identical write is a no-op. Stale writes return `preference_conflict`.

### 2.4 Ten execution classes

The fixed execution matrix remains:

| Tool | Read-only | Destructive | Idempotent | Open world |
|---|---:|---:|---:|---:|
| `call_read_only_closed` | true | n/a | n/a | false |
| `call_read_only_open` | true | n/a | n/a | true |
| `call_additive_closed` | false | false | false | false |
| `call_additive_closed_idempotent` | false | false | true | false |
| `call_additive_open` | false | false | false | true |
| `call_additive_open_idempotent` | false | false | true | true |
| `call_destructive_closed` | false | true | false | false |
| `call_destructive_closed_idempotent` | false | true | true | false |
| `call_destructive_open` | false | true | false | true |
| `call_destructive_open_idempotent` | false | true | true | true |

Downstream ToolAnnotations are normalized with MCP defaults:

```text
readOnlyHint: false
destructiveHint: true
idempotentHint: false
openWorldHint: true
```

For read-only tools, destructive/idempotent hints do not affect executor classification.

Semantic enrichment and user preferences can never change the execution class.

## 3. Authoritative, derived, and user-authored layers

V2 maintains three distinct layers.

### 3.1 Authoritative Source Contract

The downstream MCP contract is authoritative, including:

- server identity/context;
- name/title/description/icons;
- input and output schemas;
- annotations;
- `_meta`;
- exact downstream invocation identity.

Generated data never replaces or rewrites this contract.

### 3.2 Semantic Enrichment

Agent-generated Derived Router Guidance improves semantic selection only. It may describe:

- purpose;
- use/avoid conditions;
- user-intent examples;
- argument guidance;
- preconditions;
- output interpretation;
- related/alternative tools;
- capability hierarchy.

Enrichment is two-stage:

1. tool-level enrichment;
2. capability reconciliation that normalizes the global hierarchy and merges near-synonymous taxonomy branches.

### 3.3 Routing Preferences

Routing Preferences are explicit user-authored ranking guidance, separate from source contracts and base enrichment.

They may be:

- Global;
- scoped to a named Routing Profile;
- server-level;
- tool-set-level;
- conditional tool preferences.

Precedence is deterministic:

```text
active Routing Profile > Global
conditional tool preference > tool-set preference > server preference
```

Conflicts at equal scope/specificity require review rather than newest-wins behavior.

Preferences are guidance, not authorization. An explicit compatible request may still select a lower-ranked tool. Actual exclusion is controlled by server/tool availability or explicit security/configuration state.

Preference changes advance a separate monotonic `preference_revision`; they do not invalidate the semantic Index Generation or Routing State Hash. Ranking caches include effective profile + preference revision.

A preference target is bound to:

```text
Server ID + downstream tool name + preference-assumption fingerprint
```

The assumption fingerprint covers the relevant semantic-source fingerprint and normalized executor class. Removed, renamed, materially changed, or reclassified tools mark the preference `needs_review`; preferences are never silently transferred to semantically similar replacement tools.

## 4. Ambiguity Review

When many overlapping tools make a ranking choice genuinely ambiguous, indexing may produce an `ambiguity_review` batch instead of inventing a user preference.

The connected agent presents:

- competing tools;
- source-grounded pros/cons;
- conditional use cases;
- suggested preference choices.

The user may:

- choose a Global preference;
- choose a profile-specific preference;
- define a conditional preference;
- choose neutral/no preference;
- defer the review.

Open reviews do not block an otherwise-valid generation. They remain retrievable after commit and can be resolved later through `set_routing_preferences` without reindexing.

Pros/cons must be grounded in source contracts, accepted enrichment, or measured behavior; the agent must not invent differences merely to present balanced options.

## 5. Routing freshness model

### 5.1 Routing Revision

Maintain a monotonic `routing_revision` for ordering/diagnostics.

### 5.2 Routing State Hash

Correctness is proven by a deterministic `routing_state_hash`, not by revision alone.

The hash covers routing-relevant configuration plus keyed routing-secret state. It excludes purely operational secrets such as routine OAuth token refreshes and Manager Tunnel credentials.

A committed generation is routable only when its recorded Routing State Hash matches current state and its integrity checks pass.

### 5.3 Secret fingerprints

Routing-relevant resolved secrets are represented using an installation-scoped HMAC/fingerprint mechanism. Raw secret values and unkeyed secret hashes are never persisted.

Automatic OAuth access/refresh-token rotation does not alter routing state. Explicit reconnect/logout/account/scope changes, static credential replacement, or routing-relevant environment-secret changes do.

### 5.4 Preference revision is separate

`preference_revision` is not part of `routing_state_hash` because preferences affect ranking rather than source correctness or execution safety.

### 5.5 Fail-closed boundary

Normal search/detail/execution returns `index_required` when no current valid generation exists.

A valid active generation remains usable while an explicitly requested replacement staging generation is being built. Routing is disabled only if the active generation becomes stale/corrupt, not merely because staging exists.

Pure downstream availability failure does not stale an otherwise-current index. The affected call returns `downstream_unavailable`. A discovered tool-contract change does stale routing globally.

## 6. Index generations and incremental invalidation

Index construction uses persistent staging generations and atomic promotion.

A staging generation survives Manager restart when its Routing State Hash still matches. Accepted batch work remains usable. If state changed, staging becomes superseded while content-addressed artifacts whose dependencies still match may be reused.

Required artifacts include:

- authoritative source records;
- lexical representation;
- base embeddings;
- semantic enrichment;
- enriched embeddings;
- capability hierarchy;
- dependency records.

Incremental invalidation must handle not only explicit reverse dependencies but also semantic-neighborhood membership changes. Store deterministic neighborhood/context hashes so a new or changed tool that enters another tool's top-K enrichment neighborhood invalidates the affected enrichment even when no prior reverse edge existed.

Index promotion is one SQLite transaction that verifies:

- Routing State Hash still matches;
- required generation members are complete;
- required enrichment/reconciliation work is complete;
- embedding/model identity is consistent;
- dependency closure is valid;
- integrity checks pass.

Open Ambiguity Reviews do not block promotion.

A corrupt catalog/index database is quarantined for diagnostics and rebuilt from source; do not automatically salvage partially trusted routing data.

## 7. Index triggering and lifecycle interaction

Ordinary config save does not automatically launch an index rebuild or embedding calls. It marks routing stale. Rebuild begins only through `index_refresh` or an explicit native Prepare/Reindex action.

Discovery behavior by lifecycle mode:

- **Always On**: use the maintained runtime.
- **Managed**: acquire a temporary discovery/index lease, start if needed, then allow normal idle behavior after release.
- **Manual**: if stopped, index refresh reports `manual_server_stopped_for_index`; the user must start it from the native UI.
- **Disabled**: excluded from committed routing membership.

Manual blockers are surfaced explicitly rather than silently excluding enabled Manual entries.

## 8. Downstream MCP client subsystem

Add `internal/downstream` and make GPT Tunnel Manager a direct MCP client.

Supported transports:

- Stdio;
- Managed HTTP;
- External HTTP.

### 8.1 Stdio

Use a dedicated MCP stdio process/transport adapter. Do not reuse the current generic process wrapper unchanged because current stdout consumption conflicts with MCP protocol ownership.

Preserve:

- executable + argv only, no shell;
- working directory;
- environment and secret injection;
- Unix process groups;
- Windows process-tree ownership;
- stderr logging/redaction;
- bounded graceful shutdown.

### 8.2 Managed HTTP

Manager launches/owns the HTTP process and connects to it as an MCP client. Child and session teardown are one ownership unit.

### 8.3 External HTTP

Manager connects to the external MCP endpoint but never owns or terminates the remote service.

### 8.4 Downstream authentication

HTTP downstreams may use:

- MCP OAuth;
- secret-backed static Authorization/API header mode.

Interactive OAuth connect/reconnect belongs to the native UI. OAuth state/tokens use a Server-ID-scoped internal secret namespace and never enter config JSON, index text, enrichment batches, or Manager MCP calls.

Static credentials may intentionally share `secret://` references. Restrict unsafe user-configurable headers such as `Host`, `Content-Length`, and other transport-controlled headers.

Credential-bearing remote HTTP requires HTTPS except loopback. An advanced per-endpoint `Allow insecure credential transport` override may explicitly permit remote plaintext HTTP.

## 9. Live tool-contract drift

On every downstream MCP session establishment, retrieve and fingerprint the complete `tools/list` contract.

If the server advertises reliable tool-list-change notifications, use them for session-lifetime invalidation. If it does not, revalidate the tool-list fingerprint before every routed execution.

If the live fingerprint differs from the committed contract:

1. do not dispatch the requested downstream tool;
2. mark the affected routing partition dirty;
3. advance routing state/revision;
4. return `index_required`.

This guarantee assumes protocol-compliant change notifications when a server advertises them.

## 10. Upstream MCP protocol compatibility

One Manager MCP URL serves both modern and legacy clients through protocol-aware dispatch:

- MCP 2026-07-28 uses the stateless Streamable HTTP path;
- older initialize-based revisions use a stateful session path where required for server-to-client callbacks.

Both paths route into the same router implementation and expose the same logical 19-tool contract.

Compatibility proof is an early phase, not a late release-only test.

## 11. Tool-workflow compatibility

V2 remains tool-centric but bridges MCP primitives transitively required to complete tool calls.

### 11.1 Modern MRTR

Bridge supported multi-round-trip/input-required behavior associated with the originating call.

### 11.2 Legacy callbacks

For legacy sessions, support required elicitation/sampling/roots compatibility. Serialize callback-capable routed calls per legacy downstream session so callbacks map unambiguously to one upstream request. Forward only capabilities actually supported by the upstream client; otherwise return a stable unsupported-capability error.

Do not introduce new product dependence on deprecated sampling/roots; they exist for compatibility only.

### 11.3 Tasks

Proxy downstream MCP Tasks as Manager-owned opaque task IDs. Persist task mappings so polling can resume after reconnect/restart.

A Managed Use Lease remains active until the downstream task reaches a terminal state (`completed`, `failed`, `cancelled`) or expires. Managed servers must not idle-stop while an outstanding task requires them.

### 11.4 Resource links

If a downstream tool returns follow-up resource links, rewrite only the URI into an authenticated Manager-owned opaque resource reference. Preserve the original downstream URI internally. A later `resources/read` resolves the reference, reacquires the correct downstream runtime, forwards the original URI, and returns the resource.

Embedded resources remain unchanged.

## 12. Execution handles

`get_tool` returns an HMAC-authenticated self-contained Execution Handle rather than creating a mutable server-side handle table.

The handle binds at least:

- active generation;
- Server ID;
- downstream tool name;
- authoritative source fingerprint;
- normalized executor class;
- Manager process epoch/instance nonce.

The HMAC key/process epoch is ephemeral, so restarting GPT Tunnel Manager deliberately invalidates outstanding handles even when the semantic generation remains current. `get_tool` is cheap and may simply mint a new handle.

Executors also receive/display a validated human-readable tool identity for confirmation/logging UX, while routing authority still comes only from the authenticated handle.

## 13. Execution and mutation concurrency

Router hot paths must not be serialized behind the current Registry-wide lifecycle mutex. Refactor lifecycle acquisition to per-server synchronization so unrelated servers can route concurrently.

While a Server Entry has active use leases, reject routing/runtime-affecting edit, disable, or delete operations with:

```text
server_busy
active_call_count: N
```

Do not queue or force those mutations. Non-routing settings may still change.

This avoids converting a known in-flight call into an outcome-unknown state through administrative teardown.

## 14. Stable result/error semantics

Preserve downstream `CallToolResult` fidelity, including:

- text/image/audio/content blocks;
- resource links/embedded resources;
- `structuredContent`;
- downstream `isError`.

Post-dispatch errors carry an explicit outcome state:

- `not_started`;
- `completed`;
- `outcome_unknown`.

Unknown writes are never marked safely retryable.

Examples:

- oversized completed response: `result_too_large`, `outcome=completed`, `retryable=false`;
- post-call validation failure: `downstream_result_invalid`, preserve available original result, include original downstream `isError`, `retryable=false`;
- connection lost after possible dispatch: `outcome_unknown`, `retryable=false` for ambiguous calls.

Initial v2 performs no automatic ambiguous tool-call replay.

## 15. JSON Schema handling

Downstream tool schemas are untrusted.

Manager-side validation may resolve in-document/local `#` references and `$defs`, but must never perform arbitrary network or file resolution for external `$ref` values.

Tools requiring unresolved external references are surfaced as incompatible/unsupported for safe routing rather than allowing schema validation to trigger SSRF or arbitrary outbound access.

## 16. Embeddings

Provide an embedding-provider abstraction with at least one OpenAI-compatible embeddings implementation.

Settings include:

- provider type;
- base URL;
- model;
- optional dimensions;
- dedicated embedding credential.

Embedding credentials are separate from the Manager Tunnel Runtime API key.

Catalog/tool embeddings are persistent content-addressed artifacts. User search-query embeddings are **memory-only by default** in a bounded LRU and are not persisted across restart.

Exact cosine similarity is the initial vector search implementation. Benchmark larger catalogs before adding ANN.

Initial supported end-to-end enrichment target is approximately 1,000-2,000 tools; vector/index benchmarks still cover 5,000 and 10,000 tools to establish headroom and identify when the support target can be raised.

## 17. Search/ranking

Use multiple retrieval signals:

- lexical/BM25;
- source-description vectors;
- schema/input vectors;
- enrichment purpose/use-when vectors;
- user-intent vectors;
- capability vectors.

Fuse independent rankings with weighted reciprocal-rank fusion, then apply bounded enrichment and Routing Preference adjustments.

Do not force `limit` results when candidates fail the no-match relevance threshold. Returning zero or fewer than requested is valid.

`search_tools` must not start/query downstream servers; it uses only the committed index plus the configured embedding provider for the current query.

## 18. Search-quality release gate

Before execution routing is considered ready, the curated evaluation suite must achieve at least:

- critical must-route regression cases: **100% correct within top 5**;
- general curated corpus: **>=90% top-1 accuracy**;
- general curated corpus: **>=98% top-5 accuracy**;
- no-match corpus: **<=2% false-positive rate**;
- applicable explicit Routing Preference tests: **100% adherence**;
- executor-class/safety mapping: **100%**.

These are minimum v2 release gates, not permanent ceilings.

## 19. Semantic enrichment concurrency

There is one staging generation/refresh coordinator.

Enrichment batches are immutable/unclaimed work items. Multiple connected agents may receive the same pending batch. The first valid submission is accepted; an identical repeat is idempotent; conflicting repeats are rejected.

Do not add agent-session lease machinery unless later measurement proves it necessary.

## 20. Local Manager MCP endpoint and access protection

The local Manager MCP binds loopback only on a stable persisted port.

Local Manager Access Protection is enabled by default using an installation-scoped random capability token stored through the secret store. Native Settings may explicitly disable it, intentionally leaving the loopback MCP unauthenticated.

Prefer an Authorization header if the Manager Tunnel/client path can safely inject it; otherwise use an unguessable capability path. Browser `Origin` rejection remains enforced in either mode.

This is lightweight local protection, not a Manager OAuth/Auth Gateway.

## 21. Clean v2 configuration break

Do **not** implement:

- v1 compatibility structs;
- v1->v2 field conversion;
- legacy JSON aliases solely for migration;
- migration journals/backups for schema conversion;
- runtime fallback to the v1 topology;
- binary rollback compatibility with v1 user data.

V2 has only a strict v2-native schema.

For the one major-version cutover from released v1, treat existing `config/` and v1 routing/runtime `data/` as opaque legacy state. The updater/first-launch cutover may move those directories aside as discardable `legacy-v1-<timestamp>` data before initializing clean v2 defaults, but it must not parse or convert their contents. `tools/` may remain because Manager Tunnel tooling is still used.

After v2 release, normal v2 schema migrations may be added only for future v2+ versions when actually required.

## 22. V2 configuration model

Remove active v1 fields that exist only for the old topology, including:

- per-server ChatGPT plugin name;
- per-server Tunnel ID/settings;
- lifecycle marker/skill settings.

Add v2-native settings for:

- stable local Manager MCP port;
- optional Local Manager Access Protection;
- Manager Tunnel only;
- embedding provider/model/base URL/dimensions;
- embedding credential;
- downstream auth configuration;
- per-endpoint insecure-credential-transport override;
- default Routing Profile;
- index diagnostics/reindex controls.

Server IDs remain immutable internal `srv_...` identities.

## 23. Storage model

Use Portable-Root-local SQLite for catalog/index state, with a pure-Go driver preferred for release simplicity.

Logical areas include:

- generation metadata;
- source server/tool contracts;
- content fingerprints;
- enrichment artifacts/dependencies;
- enrichment/reconciliation/review batches;
- persistent catalog embeddings;
- capability hierarchy;
- Routing Preferences/Profiles and `preference_revision`;
- task/resource continuation mappings where persistence is required;
- FTS/lexical records.

Do not persist raw user query text or query embeddings by default.

## 24. Native desktop UX

Remove per-server plugin/tunnel/marker UI.

Servers page retains the built-in non-deletable Manager MCP row and downstream Server Entries.

Add native UI for:

- local Manager MCP endpoint/port;
- Local Manager Access Protection enable/disable state;
- Manager Tunnel only;
- embedding provider and credential;
- downstream OAuth connect/reconnect/status;
- static downstream credential controls;
- insecure-transport override warnings;
- index readiness/staleness;
- dirty servers and Manual indexing blockers;
- required enrichment count;
- open Ambiguity Reviews;
- Routing Profiles and preference management;
- default Routing Profile;
- reset/reindex diagnostics.

Known secrets remain purpose-specific value controls; users should not type internal `secret://...` names for standard credential paths.

## 25. Startup sequence

1. Resolve executable and Portable Root.
2. Acquire single-instance ownership.
3. Load strict v2 configuration or initialize fresh defaults.
4. Initialize secret store/redaction/logging.
5. Open/validate catalog/index database; quarantine corrupt DB if necessary.
6. Load routing revision/state hash, preference revision, active/staging generations.
7. Initialize downstream client/auth factory.
8. Initialize server Registry/Supervisors.
9. Initialize embedding provider.
10. Initialize catalog/index/enrichment/preference/router services.
11. Start protocol-aware loopback Manager MCP on persisted port.
12. Start optional Manager Secure MCP Tunnel if configured.
13. Start enabled Always On downstream servers.
14. Start native tray/UI.
15. Start self-update/tunnel-client update checks as configured.
16. If no valid active generation exists, expose indexing/preference tools while normal routing fails closed.

Do not run semantic enrichment automatically in the background.

## 26. Shutdown sequence

1. Stop accepting new routed execution.
2. Reject/wind down new lifecycle acquisitions.
3. Cancel/close upstream sessions according to MCP semantics.
4. Preserve valid staging/task metadata needed for restart.
5. Stop Managed/Always On Manager-owned downstream runtimes cleanly.
6. Close external downstream client sessions without terminating external services.
7. Stop Manager Tunnel.
8. Flush/close catalog DB and logs.
9. Release single-instance ownership.

Never abandon process-owned children merely because one half of a Managed HTTP runtime fails.

## 27. Implementation phases

### Phase 0 — Freeze baseline and planning contract

- record v1.0.32/main baseline;
- run current CI baseline before production edits;
- retain this document + ADRs as normative v2 contract;
- confirm no production behavior has changed on the planning branch.

Exit gate: documented baseline and green existing tests/build where environment permits.

### Phase 1 — MCP compatibility spike first

Before semantic/index investment, build disposable prototypes/test servers proving:

- direct Stdio/Managed HTTP/External HTTP client connect;
- legacy initialize and MCP 2026-07-28 behavior;
- modern stateless + legacy stateful upstream dispatch;
- `tools/list` pagination/change notifications;
- structured/multimedia CallToolResult fidelity;
- cancellation;
- MRTR/input-required flows;
- legacy callback behavior;
- Tasks;
- resource-link followups;
- downstream OAuth viability with the pinned SDK.

Exit gate: compatibility inventory is proven or blockers are resolved before deeper implementation.

### Phase 2 — Clean v2 config/state foundation

- replace config structs/validation with strict v2-native schema;
- no v1 conversion code;
- implement clean-start major cutover behavior;
- stable local Manager MCP port;
- embedding/downstream-auth/local-protection/profile settings;
- routing revision/state-hash/preference-revision interfaces;
- transactional config/secret/index invalidation semantics.

Exit gate: fresh v2 config persists/validates atomically; v1 config is not interpreted.

### Phase 3 — Direct downstream MCP clients

- `internal/downstream` abstraction;
- dedicated Stdio MCP process adapter;
- Managed HTTP owned process/client;
- External HTTP client;
- OAuth/static auth;
- HTTPS/override policy;
- limits/timeouts/panic containment;
- full tools-list fingerprinting/drift hooks.

Exit gate: arbitrary test MCPs can be listed/called safely without per-server tunnel-client.

### Phase 4 — Catalog, generations, and routing state

- SQLite catalog;
- authoritative canonical contracts;
- content fingerprints;
- keyed secret fingerprints;
- active/staging generations;
- persistent staging resume;
- dirty partitions;
- dependency graph and neighborhood hashes;
- atomic promotion/integrity/quarantine behavior.

Exit gate: authoritative catalog rebuild is incremental and crash-safe.

### Phase 5 — Embedding and deterministic retrieval substrate

- provider abstraction/OpenAI-compatible provider;
- batching/dimensions/model fingerprint;
- persistent catalog embeddings;
- memory-only query LRU;
- lexical/FTS;
- exact cosine search;
- 1k/5k/10k benchmark measurements.

Exit gate: deterministic retrieval substrate passes vector/lexical correctness tests and recorded performance budget.

### Phase 6 — Agent enrichment, capability reconciliation, preferences

- bounded tool enrichment batches;
- semantic-neighbor construction;
- neighborhood-change invalidation;
- capability reconciliation stage;
- non-blocking Ambiguity Reviews;
- multi-agent idempotent submission behavior;
- Routing Profiles/Preferences persistence;
- preference conflict/review semantics;
- preference management tools/service.

Exit gate: required enrichment can complete incrementally; optional ambiguity feedback can be saved/changed without reindex.

### Phase 7 — Search/detail and quality gate

- multi-facet retrieval;
- RRF fusion;
- no-match threshold;
- profile/preference ranking overlay;
- `search_tools` compact result contract;
- `get_tool` authoritative/derived separation;
- self-contained Execution Handle minting;
- curated evaluation corpus and thresholds.

Exit gate: all Section 18 retrieval-quality thresholds pass before execution routing is enabled.

### Phase 8 — Execution router

- ten executor registrations;
- annotation normalization/class validation;
- handle/HMAC/process-epoch validation;
- safe argument validation with no remote `$ref` fetch;
- outcome-aware error contract;
- result fidelity/size limits;
- no ambiguous replay.

Exit gate: safety/executor matrix tests are 100%; representative tools round-trip without result loss.

### Phase 9 — Router-native lifecycle

- per-server acquisition locks, no global hot-path lifecycle mutex;
- Managed Use Leases;
- automatic Managed start/idle-stop;
- task-held leases;
- Manual/Disabled semantics;
- `server_busy` mutation protection;
- tool-drift invalidation hooks;
- direct activity timestamps instead of tunnel telemetry.

Exit gate: Managed lifecycle works entirely from routed use with no lifecycle skill or downstream tunnel telemetry.

### Phase 10 — Full Manager MCP upstream surface

- protocol-aware modern/legacy dispatch;
- fixed 19-tool contract;
- optional local capability protection;
- browser-Origin rejection;
- resource continuation proxy;
- Task proxy/mappings;
- legacy callback serialization.

Exit gate: representative upstream clients see stable annotations and complete tool workflows.

### Phase 11 — Native desktop migration

- remove per-server tunnel/plugin/marker fields/actions;
- add embedding/index/profile/preference/auth UX;
- add Manager local protection setting;
- add OAuth connect/reconnect UX;
- index blockers/reviews/status;
- retain existing native tray/logging/update behavior unless intentionally changed.

Exit gate: all new v2 configuration is manageable without editing JSON or secret references manually.

### Phase 12 — Remove old topology

Delete/retire:

- `internal/lifecycleskill/`;
- `internal/marker/`;
- `assets/lifecycle-skill/`;
- per-server tunnel runtime integration;
- old four-tool lifecycle Manager MCP implementation;
- obsolete v1 config fields/code.

Keep `internal/tunnelclient/` only for Manager Tunnel install/update/runtime.

Exit gate: searches show no active downstream dependency on per-server tunnels/markers/lifecycle skill.

### Phase 13 — Full verification and release hardening

- unit/integration/race/failure-injection tests;
- real representative MCP servers;
- modern/legacy protocol suite;
- OAuth/credential transport tests;
- index crash/restart/corruption tests;
- preference conflict/review tests;
- search eval gate;
- all six native target builds;
- `go mod tidy` zero diff;
- `go test ./...`;
- `go vet ./...`;
- release packaging/self-update clean-break test.

Exit gate: Definition of Done satisfied and no unresolved compatibility blocker remains.

## 28. Required checkpoints

### Checkpoint A — Compatibility proof

Complete Phase 1 before building the semantic system.

### Checkpoint B — Direct client proof

Prove `Manager -> ListTools -> CallTool` for all three transports without per-server tunnel-client.

### Checkpoint C — Incremental index proof

Prove:

```text
routing-relevant edit
-> stale
-> incremental refresh
-> tool enrichment
-> capability reconciliation
-> atomic commit
```

with large synthetic catalogs and neighborhood-change cases.

### Checkpoint D — Permission proof

Before old topology deletion, connect representative MCP clients and verify the intended 10-class annotation matrix plus the two preference-tool annotations.

### Checkpoint E — Lifecycle/task proof

Prove Managed auto-start, active-call protection, task-held leases, idle-stop, and crash cleanup using routed calls only.

### Checkpoint F — Real-agent proof

Using representative agents and multiple real MCP servers, measure:

- indexing completion;
- ambiguity review UX;
- profile preference behavior;
- correct search/get_tool/executor selection;
- exact argument schema use;
- result fidelity;
- task/resource continuation behavior.

## 29. Security requirements

- loopback bind only for local Manager MCP;
- optional capability protection enabled by default;
- browser-Origin rejection always;
- no arbitrary routing URLs/executables supplied through Manager tools;
- downstream descriptions/schemas treated as untrusted data;
- enrichment/preferences cannot authorize or weaken permission classes;
- bounded discovery/batch/result sizes;
- no remote `$ref` resolution;
- no raw secret persistence/indexing;
- keyed routing-secret fingerprints only;
- HTTPS for credential-bearing remote endpoints unless explicit override;
- query embeddings/text not persistently cached by default;
- remote embedding disclosure is surfaced in UI/docs.

## 30. Definition of Done

GPT Tunnel Manager v2 is done when a fresh v2 user can:

1. launch into a clean v2-native configuration;
2. optionally enable/configure one Manager Secure MCP Tunnel;
3. optionally keep or disable default local Manager capability protection;
4. configure an embedding provider;
5. add Stdio, Managed HTTP, and External HTTP MCP servers without per-server tunnels/plugins;
6. configure downstream OAuth/static authentication where needed;
7. connect one MCP-capable agent to the Manager MCP;
8. receive `index_required` until a valid semantic generation exists;
9. complete incremental tool enrichment + capability reconciliation;
10. optionally resolve or defer Ambiguity Reviews;
11. define Global or profile-specific Routing Preferences;
12. search natural-language intent with the quality thresholds in Section 18;
13. inspect exact authoritative tool contracts with `get_tool`;
14. receive a generation/tool/class-bound Execution Handle;
15. execute through the correct permission-class tool;
16. automatically start the correct Managed server;
17. preserve downstream structured/multimedia/error results;
18. bridge required Tasks/resource followups/MRTR or legacy callbacks;
19. hold Managed runtimes while active calls/tasks require them;
20. idle-stop Managed runtimes later;
21. detect live tool-contract drift before unsafe dispatch;
22. mark routing stale after routing-relevant config/credential/source changes;
23. rebuild only the safe incremental dependency closure;
24. continue using an old valid generation during an optional replacement build;
25. atomically promote a coherent replacement generation;
26. change Routing Preferences immediately without semantic reindexing;
27. survive restart with valid committed/staging state while invalidating old Execution Handles;
28. recover from corrupt index data by quarantining/rebuilding rather than trusting salvage.

At no point should ordinary v2 use require:

- a per-server Tunnel ID;
- a per-server Developer Plugin;
- a lifecycle marker;
- a lifecycle skill;
- ChatGPT-side start/wait/shutdown choreography;
- manual internal secret-reference names;
- v1 configuration conversion.
