# GPT Tunnel Manager Context

GPT Tunnel Manager is a portable desktop MCP aggregation and lifecycle control plane. It exposes one upstream Manager MCP and directly connects to configured downstream MCP servers while preserving their authoritative tool contracts.

## Language

**GPT Tunnel Manager**:
The desktop application that owns Server Entries, the Manager MCP, downstream MCP client runtimes, lifecycle policy, the semantic Tool Catalog, and the optional Manager Tunnel.
_Avoid_: Tunnel Manager when the shorter name could be confused with a tunnel runtime

**Manager MCP**:
The single upstream MCP surface exposed by GPT Tunnel Manager to ChatGPT or another MCP-capable agent harness. It exposes a fixed 19-tool surface covering indexing, discovery, tool detail, ten permission-class execution tools, and routing-preference management.
_Avoid_: Manager Developer Plugin, lifecycle MCP

**Downstream MCP**:
A configured MCP server that GPT Tunnel Manager connects to as an MCP client and whose tools may be discovered and routed through the Manager MCP.
_Avoid_: participating plugin, per-server plugin

**Server Entry**:
The persisted GPT Tunnel Manager identity and configuration for one Downstream MCP.

**Server ID**:
The immutable generated `srv_...` identifier for one Server Entry. It is an internal stable identity for configuration, indexing, lifecycle, diagnostics, and routing dependencies.

**Server Mode**:
The lifecycle policy of a Server Entry: Always On, Managed, or Manual.

**Always On**:
A Server Mode in which an enabled Downstream MCP is kept running while GPT Tunnel Manager is active.

**Managed**:
A Server Mode in which GPT Tunnel Manager may automatically start the Downstream MCP for routed work and later stop it after its idle policy permits.

**Manual**:
A Server Mode in which lifecycle start/stop control remains exclusively with the native UI.

**Enabled State**:
Whether a Server Entry participates in runtime and routing policy. Disabled entries are excluded from committed routing generations.

**Desired State**:
Whether GPT Tunnel Manager currently intends an owned Downstream MCP runtime to be running or stopped.

**Observed State**:
The lifecycle condition GPT Tunnel Manager currently observes for a Downstream MCP runtime, such as Stopped, Starting, Ready, Degraded, Retry Wait, or Stopping.

**Managed Use Lease**:
A temporary claim representing active routed work against a Managed Downstream MCP. A Managed runtime cannot idle-stop while one or more use leases are active.
_Avoid_: tunnel activity lease, telemetry lease

## Routing and indexing

**Authoritative Source Contract**:
The exact downstream MCP metadata GPT Tunnel Manager discovered for a tool, including its name, title, description, schemas, annotations, `_meta`, and server context. Router-generated metadata never replaces it.
_Avoid_: generated schema, enriched contract

**Derived Router Guidance**:
Structured semantic metadata generated to improve discovery and tool choice without changing the Authoritative Source Contract or permission classification.
_Avoid_: authoritative metadata

**Tool Catalog**:
The persistent collection of authoritative Downstream MCP tool contracts and their routing-derived artifacts.
_Avoid_: dynamic upstream tool list

**Index Generation**:
A coherent immutable view of Tool Catalog membership and routing artifacts that is built in staging and atomically promoted to active.

**Routing Revision**:
A monotonic ordering value advanced by routing-relevant mutations. It is diagnostic/order metadata, not by itself the freshness proof for an Index Generation.

**Routing State Hash**:
The deterministic identity of routing-relevant configuration and keyed routing-secret state that an Index Generation was built against. Routing is allowed only when the active generation matches the current routing state.

**Semantic Enrichment**:
Bounded structured Derived Router Guidance produced by the connected agent from authoritative tool records and selected semantic neighbors.

**Routing Preference**:
A persisted user-authored ranking rule that prefers, avoids, or conditionally chooses one Server Entry or tool set over another. Routing Preferences are a separate overlay from Authoritative Source Contracts and Semantic Enrichment; they may affect discovery ranking and selection explanations but can never change schemas, permissions, executor classes, or authorization.

**Routing Profile**:
A named scope containing Routing Preferences for a particular project, workflow, or context. A Global layer applies everywhere; an explicitly active Routing Profile overrides Global rules where more specific profile rules apply. Agents may select a profile when they know the current context, but GPT Tunnel Manager does not silently infer project identity when uncertain.
_Avoid_: automatic project identity

**Ambiguity Review**:
An optional, non-blocking user-feedback step raised when overlapping tools cannot be ranked confidently from authoritative metadata and semantic enrichment alone. The connected agent presents the competing choices with source-grounded pros, cons, and conditional use cases, then records the user's decision as a Routing Preference or records that neutral behavior should remain. Unresolved reviews are marked `needs_user_feedback` and do not block an otherwise valid index generation.

**Preference Review State**:
A Routing Preference whose referenced tools or assumptions no longer match current catalog state is marked `needs_review` and is not silently transferred to a replacement tool. Conflicts at the same scope and specificity likewise require review.

**Embedding Provider**:
The separately configured service or compatible local endpoint used to generate Tool Catalog and search-query embeddings. Its credentials are distinct from the OpenAI Runtime API key used by the Manager Tunnel.

**Tool Reference**:
An opaque generation-bound reference returned by discovery and accepted by `get_tool` to identify one cataloged downstream tool without exposing routing internals as selectors.

**Execution Handle**:
An authenticated generation-bound capability minted by `get_tool` for one exact Authoritative Source Contract and expected Execution Class. It is required to execute the selected downstream tool.

**Execution Class**:
One of the fixed upstream permission categories derived only from normalized downstream MCP ToolAnnotations. The class determines which Manager MCP executor may invoke a tool.
_Avoid_: semantic permission, enrichment permission

## Transport and exposure

**Transport Type**:
How GPT Tunnel Manager connects to a Downstream MCP: Stdio, Managed HTTP, or External HTTP.

**Stdio**:
A Transport Type where GPT Tunnel Manager launches and owns the configured MCP process and communicates with it directly over MCP stdio.

**Managed HTTP**:
A Transport Type where GPT Tunnel Manager launches and owns the configured HTTP MCP process and connects to its configured MCP URL.

**External HTTP**:
A Transport Type where GPT Tunnel Manager connects to an independently owned MCP HTTP endpoint and never terminates that external service.

**Manager Tunnel**:
The optional OpenAI Secure MCP Tunnel exposing only the Manager MCP to remote ChatGPT access. Downstream MCPs do not receive individual tunnels in v2.
_Avoid_: per-server tunnel

**Downstream Authentication**:
Credentials or OAuth state used by GPT Tunnel Manager as an MCP client when a configured HTTP Downstream MCP requires authentication. This is separate from Manager-layer authentication and from the Manager Tunnel Runtime API key.

**Local Manager Access Protection**:
Optional installation-scoped capability-token protection for the loopback Manager MCP. It is enabled by default but may be disabled in native settings to intentionally permit unauthenticated local MCP access.
_Avoid_: Manager OAuth, Auth Gateway

**Portable Root**:
The writable directory under which GPT Tunnel Manager stores configuration, runtime data, index data, managed tunnel-client versions, instance metadata, and optional logs.
