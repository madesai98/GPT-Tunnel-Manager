# GPT Tunnel Manager v2 — Implemented Architecture Contract

Status: **Implemented and verified for v2.0.0**  
Release line: **v2.0.0**  
Implementation branch: `feature/v2-mcp-router`  
Historical v1 planning baseline: `08366ffbd299177870c10a3446ab9e4dcd35a18e` (`Release v1.0.32`)

This document is the canonical v2 product/architecture contract. The implementation was completed in Phases 0–13. `docs/IMPLEMENTATION_STATUS.md` records the release verification state, while `CONTEXT.md` and ADR 0009 onward define the accepted terminology and architectural decisions.

## 1. Product boundary

GPT Tunnel Manager v2 is a single MCP aggregation, compression, discovery, preference, execution, and lifecycle gateway.

It owns:

1. one upstream **Manager MCP** server; and
2. direct MCP client connections to configured downstream MCP servers.

V2 does not create a per-server Secure MCP Tunnel, ChatGPT Developer Plugin, lifecycle marker, lifecycle skill, or downstream `tunnel-client` process. `tunnel-client` remains only for the optional single Manager Secure MCP Tunnel.

V2 is intentionally a clean product/configuration break. It does not parse or convert v1 configuration or routing state into v2 state.

## 2. Fixed Manager MCP contract

The Manager MCP exposes a stable **19-tool** surface regardless of downstream tool count.

### Indexing and enrichment

- `index_status`
- `index_refresh`
- `index_get_enrichment_batch`
- `index_submit_enrichment_batch`
- `index_commit`

Required enrichment work consists of tool enrichment plus capability reconciliation. Ambiguity Reviews are optional and non-blocking; an otherwise-ready generation may commit while reviews remain open.

### Discovery and detail

- `search_tools`
- `get_tool`

`search_tools` operates only on the committed semantic index. `get_tool` returns the exact authoritative downstream tool contract separately from derived semantic guidance and returns the authenticated Execution Handle needed for execution.

### Routing preferences

- `get_routing_preferences`
- `set_routing_preferences`

Routing Preferences are a user-authored ranking overlay. They use an independent `preference_revision`, optimistic conflict detection, deterministic Global/profile precedence, and `needs_review` behavior when referenced tool assumptions change.

### Permission-preserving execution classes

The ten fixed execution tools are:

- `call_read_only_closed`
- `call_read_only_open`
- `call_additive_closed`
- `call_additive_closed_idempotent`
- `call_additive_open`
- `call_additive_open_idempotent`
- `call_destructive_closed`
- `call_destructive_closed_idempotent`
- `call_destructive_open`
- `call_destructive_open_idempotent`

The execution class is derived only from normalized downstream MCP ToolAnnotations. Semantic enrichment and Routing Preferences cannot change permissions or move a tool into a weaker execution class.

## 3. Downstream transports and lifecycle

Supported downstream transports are:

- **Stdio** — GPT Tunnel Manager launches and owns the MCP process and protocol stdio.
- **Managed HTTP** — GPT Tunnel Manager launches/owns the HTTP MCP process and connects to its configured MCP endpoint.
- **External HTTP** — GPT Tunnel Manager connects to an independently owned MCP endpoint and never terminates that service.

Supported Server Modes are:

- **Always On** — maintained while GPT Tunnel Manager is active.
- **Managed** — automatically started for routed/index work and idle-stopped later.
- **Manual** — only explicit native lifecycle controls may start/stop it.
- **Disabled** — excluded from acquisition and committed routing membership.

Managed Use Leases protect active calls and downstream tasks from idle-stop. Acquisition synchronization is per-server rather than registry-wide, so unrelated servers may route concurrently. Routing/runtime mutations against a server with active use fail with `server_busy` and an accurate `active_call_count`.

Manager-owned activity timestamps drive Managed idle behavior; tunnel-client telemetry is not part of downstream lifecycle.

## 4. Tool Catalog, indexing, and routing freshness

V2 maintains a Portable-Root-local SQLite Tool Catalog containing authoritative downstream contracts and derived routing artifacts.

Indexing uses persistent staging generations and atomic promotion. A committed generation is routable only when its recorded deterministic `routing_state_hash` matches current routing-relevant configuration/secret state and integrity checks pass.

The index includes:

- authoritative server/tool contracts;
- deterministic lexical/BM25 records;
- source-description and input-schema embeddings;
- semantic enrichment and enriched embeddings;
- capability hierarchy/reconciliation artifacts;
- dependency and semantic-neighborhood identities;
- Routing Profiles/Preferences and review state;
- continuation mappings where persistence is required.

Semantic-neighborhood membership participates in invalidation, so a new or changed tool entering another tool's enrichment neighborhood invalidates affected derived guidance even when no previous reverse dependency existed.

Search-query embeddings are memory-only by default. Persistent embeddings are content-addressed and bound to provider/model/config/projection identity.

A corrupt catalog/index is quarantined and rebuilt rather than treated as trusted partial routing state.

## 5. Enrichment, ambiguity, and preferences

Enrichment batches are immutable work items that multiple connected agents may receive. The first valid submission wins; identical retries are idempotent; conflicting submissions are rejected.

Capability reconciliation normalizes overlapping semantic taxonomy across tool batches. Open Ambiguity Reviews do not block index promotion and may be resolved later into Global or profile-scoped Routing Preferences without semantic reindexing.

Routing Preference precedence is deterministic:

```text
explicit request profile > configured default profile > Global only
profile scope > Global scope
conditional tool preference > tool-set preference > server preference
```

Equal-scope/equal-specificity conflicts require review instead of newest-wins behavior. Preferences are guidance, not authorization.

## 6. Execution safety

`get_tool` mints an HMAC-authenticated Execution Handle bound to the active generation, Server ID, downstream tool name, authoritative source fingerprint, executor class, and Manager process epoch.

Executors fail closed when the handle, generation, source fingerprint, executor class, input schema, or live downstream tool contract does not match expectations.

Manager-side JSON Schema validation resolves only local/in-document references. Arbitrary remote or filesystem `$ref` resolution is never performed.

Downstream execution preserves MCP result fidelity, including text, images, audio, structured content, embedded resources, resource links, and downstream `isError`.

Post-dispatch errors distinguish `not_started`, `completed`, and `outcome_unknown`. Ambiguous tool calls are never automatically replayed.

Live `tools/list` drift marks routing stale and returns `index_required` before another unsafe dispatch.

## 7. Protocol compatibility and continuations

One loopback `/mcp` URL supports both modern MCP 2026-07-28 stateless operation and older initialize-based stateful sessions where callback compatibility is required.

The Manager owns continuation mappings needed to complete tool workflows:

- downstream Tasks are exposed through Manager-owned opaque task IDs and task-held Managed leases;
- downstream resource links are rewritten to authenticated Manager-owned opaque references for later `resources/read` forwarding;
- modern multi-round-trip/input-required behavior is bridged;
- legacy elicitation, sampling, and roots callbacks are supported only where required for compatibility and are serialized when needed for unambiguous routing;
- cancellation propagates through routed work.

These compatibility layers do not reintroduce the deleted v1 lifecycle topology.

## 8. Authentication and security boundaries

The following credential domains remain separate:

- local Manager capability protection;
- optional Manager Secure MCP Tunnel Runtime API key;
- embedding-provider credential;
- downstream static HTTP credentials;
- downstream OAuth state/tokens;
- secret-backed downstream environment values.

Credential values are stored through the platform secret store and are not persisted directly in `manager.json`, `servers.json`, semantic index text, or enrichment batches.

Local Manager capability protection is enabled by default. Browser-Origin requests are rejected regardless of whether local capability protection is enabled.

Credential-bearing remote downstream HTTP requires HTTPS unless the Server Entry explicitly enables the insecure transport override.

The optional Manager Tunnel receives its local Authorization capability and Runtime API key through environment-backed references rather than secret-bearing argv values.

## 9. Native desktop application

The v2 native Gio application provides:

- Server Entry add/edit/delete for all transports and modes;
- Start/Stop/Restart controls using the router-native lifecycle service;
- downstream OAuth connect/reconnect/disconnect;
- static-header and secret-environment configuration;
- embedding provider/model/base URL/dimension/credential configuration;
- Index status, refresh, enrichment work, Ambiguity Reviews, and commit controls;
- Routing Profile and Routing Preference management;
- local Manager port/capability protection settings;
- optional single Manager Tunnel setup/status;
- structured log filtering, clear, text/JSONL export, and redaction;
- tunnel-client install/update/rollback for the Manager Tunnel only;
- launch-at-login, tray/start-hidden/close behavior, appearance, disk logging, and application self-update.

No normal v2 setup workflow requires a downstream Tunnel ID, Developer Plugin, lifecycle marker, lifecycle skill, or manually typed internal `secret://` reference.

## 10. Clean v1-to-v2 break and self-update

The v2 application does not contain v1 configuration compatibility structs, conversion aliases, migration journals, or runtime fallback to the old topology.

During the major-version cutover, legacy v1 configuration/routing state is treated as opaque legacy data rather than parsed or converted. The Portable Root remains the user-data boundary.

Application self-update:

- downloads and SHA-256-verifies the selected release;
- stages application files in a temporary location;
- protects `config/`, `data/`, and `tools/` during replacement;
- rejects a v2 archive containing the obsolete packaged `lifecycle-skill/` directory;
- removes an existing obsolete packaged `lifecycle-skill/` directory during replacement;
- uses an independent updater process/terminal to stop, replace, restart, and exit.

## 11. Verification and release gate

The v2 release gate includes:

- committed module-graph verification (`go mod tidy` produces zero diff);
- `go test ./...`;
- `go test -race ./...` in CI;
- the dedicated Section 18-style search-quality regression gate;
- `go vet ./...`;
- exact-cosine/BM25 retrieval-scale benchmarks;
- real direct Stdio/Managed HTTP/External HTTP MCP integration coverage;
- modern/legacy Manager protocol and continuation coverage;
- OAuth/static credential and clean-break self-update tests;
- lifecycle/crash/cancellation/server-busy coverage;
- native desktop builds for Windows/Linux/macOS on amd64 and arm64;
- independent `CGO_ENABLED=0` headless cross-builds for all six OS/architecture targets;
- Windows no-console child-process and DPAPI checks.

The committed search-quality thresholds are 100% critical top-5, at least 90% general top-1, at least 98% general top-5, at most 2% no-match false positives, 100% explicit preference adherence, and 100% executor/safety mapping.

## 12. Implementation phases

All phases are complete:

0. baseline and planning contract;
1. MCP compatibility spike;
2. strict v2 config/state foundation;
3. direct downstream MCP clients;
4. catalog/generations/routing state;
5. embeddings and deterministic retrieval;
6. semantic enrichment/capability reconciliation/preferences;
7. search/detail and quality gates;
8. execution router;
9. router-native lifecycle;
10. full Manager MCP upstream surface;
11. native desktop migration;
12. removal of the old topology;
13. full verification and release hardening.

## 13. Definition of Done

V2 is complete when a fresh user can configure downstream MCPs, authentication, embeddings, indexing, Ambiguity Reviews, Routing Profiles/Preferences, discovery, exact tool inspection, permission-class execution, Managed lifecycle, continuations, optional Manager remote exposure, and native operational settings without using the deleted per-server tunnel/plugin/lifecycle system.

That Definition of Done is satisfied for the v2.0.0 release line. Detailed phase-by-phase implementation evidence and CI checkpoints are recorded in `docs/IMPLEMENTATION_STATUS.md` and repository history.
