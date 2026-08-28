# V2 MCP Compatibility Spike

Date: 2026-08-28

Status: implemented as executable compatibility coverage for Phase 1 of `docs/V2_IMPLEMENTATION_PLAN.md`.

## Scope

This spike validates the MCP protocol behaviors that the v2 router depends on before the configuration, catalog, routing, and desktop layers are built. It targets the repository-pinned `github.com/modelcontextprotocol/go-sdk v1.7.0` and does not change the accepted v2 architecture.

The focused test suite is:

```text
go test ./internal/mcpcompat -count=1
```

Repository-wide CI remains the release gate after the focused suite passes.

## Findings

### Protocol negotiation and one Manager URL

Go SDK v1.7.0 has first-class MCP `2026-07-28` support. Its client attempts `server/discover` and falls back to the legacy `initialize` handshake when modern discovery is not available. Its Streamable HTTP server must be stateless for `2026-07-28`; a stateful handler negotiates legacy revisions.

The Manager should therefore expose one HTTP URL backed by two protocol modes that share the same logical Manager router:

- a stateless Streamable HTTP handler for MCP `2026-07-28`; and
- a stateful Streamable HTTP handler for older initialize-based sessions.

The compatibility tests prove that the same URL can select the modern stateless path for a current client and the legacy stateful path for a client that requires initialize-based callbacks. The legacy path also proves elicitation, sampling, and roots callbacks.

This hybrid dispatch is a Manager transport concern. It must not duplicate routing policy or tool implementations between the two handlers.

### Direct downstream transports

The spike proves all three downstream connection shapes required by the plan:

- Stdio: a real child MCP executable is connected through the SDK `CommandTransport` and invoked successfully.
- Managed HTTP: a real child MCP executable is started in HTTP mode, announces its loopback endpoint, and is connected with `StreamableClientTransport`.
- External HTTP: an independent HTTP endpoint is connected with `StreamableClientTransport`.

`CommandTransport` is only a compatibility proof. It is not the production v2 Stdio process adapter. Phase 3 still requires the dedicated process/transport implementation from the canonical plan so GPT Tunnel Manager retains executable+argv execution, no shell, working-directory and environment handling, secret injection, Unix process groups, Windows process-tree ownership, stderr logging/redaction, bounded shutdown, and protocol ownership of stdout.

### Tool discovery and change notifications

The tests cover paginated `tools/list` consumption and list-change delivery. The modern SDK maps change delivery onto the `subscriptions/listen` model while retaining compatibility with older notification-capable sessions.

The catalog layer must still fingerprint complete authoritative tool lists rather than treating a notification itself as the new source contract.

### MRTR and legacy callbacks

Modern multi-round-trip input-required flow works through the SDK middleware. The test exercises an elicitation input request, request-state echo, automatic fulfillment, and retry.

Legacy stateful sessions independently prove server-to-client elicitation, sampling, and roots callbacks. Legacy callback-capable routed calls must still be serialized per downstream session as required by the canonical plan.

### Cancellation

Cancellation of a client tool call reaches the downstream handler context in the direct SDK path. The production router must preserve this behavior across leases, transport adapters, and task handling.

### Result fidelity and resources

`CallToolResult` preserves text, image, audio, resource-link, embedded-resource, `structuredContent`, and `isError` data. A resource link can be followed by a subsequent resource read.

The production Manager must still rewrite returned resource-link URIs into authenticated Manager-owned opaque references before exposing them upstream. Embedded resources remain unchanged.

### Downstream OAuth

`StreamableClientTransport.OAuthHandler` supports token acquisition and the expected 401/403 authorize-and-retry flow. The spike proves that a rejected unauthenticated request can trigger authorization and reconnect successfully.

This does not move interactive OAuth into the MCP surface. Per ADR 0012, interactive connect/reconnect remains desktop-owned and OAuth tokens/state belong in the Server-ID-scoped internal secret namespace.

## Tasks extension decision

Tasks are no longer a core `2026-07-28` result type in the Go SDK. They are the `io.modelcontextprotocol/tasks` extension defined by SEP-2663. A task-capable server may return a polymorphic `tools/call` result with `resultType: "task"`, and the client then drives `tasks/get`, `tasks/update`, and `tasks/cancel`.

Go SDK v1.7.0 exposes custom JSON-RPC methods, so the three task-management methods can be registered and invoked without forking the SDK. However, its standard `tools/call` API has a fixed `CallToolResult` result shape, and its custom-method API intentionally rejects attempts to shadow the standard `tools/call` method. The SDK therefore cannot, by itself, decode a task handle returned in place of `CallToolResult`.

V2 will use a small task-extension compatibility layer at the routed wire boundary:

1. advertise `io.modelcontextprotocol/tasks` only when the Manager is prepared to own the task lifecycle;
2. discriminate the raw `tools/call` result before fixed-shape `CallToolResult` decoding;
3. decode `resultType: "task"` into a task handle;
4. register/use `tasks/get`, `tasks/update`, and `tasks/cancel` through the SDK custom-method facility;
5. persist downstream-task to Manager-task mappings before exposing Manager-owned opaque task IDs;
6. poll or update the downstream task while preserving MRTR/input requests and terminal result/error fidelity; and
7. keep Managed Use Leases active while nonterminal tasks still depend on the server.

`internal/mcpcompat` contains the initial extension types, task-method registration, and polymorphic result decoder needed by later direct-client/router phases. The compatibility tests validate the extension wire shape and all three task-management methods. Full Manager-owned task persistence and lifecycle remain Phase 8/10 work as specified by the canonical plan.

No SDK upgrade is required for the spike. As of this implementation, v1.7.0 is the current stable Go SDK release and is the release that added full MCP `2026-07-28` support.

## Phase 1 exit decision

The protocol surface is implementable without changing the accepted architecture.

Known implementation requirements carried forward are:

- use hybrid modern-stateless / legacy-stateful dispatch behind one Manager URL;
- keep a dedicated production Stdio MCP process adapter rather than reusing the v1 logging process wrapper or treating `CommandTransport` as final;
- keep legacy callback calls serialized per legacy downstream session;
- add the Tasks extension compatibility layer at the raw routed result boundary; and
- keep OAuth secret/UI responsibilities outside the Manager MCP surface.

These are implementation constraints, not unresolved architecture blockers. Phase 2 may begin once this spike and repository-wide CI are green.
