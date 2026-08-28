# Keep v2 tool-centric but preserve complete tool workflows

Status: accepted

GPT Tunnel Manager v2 is primarily a tool router rather than a general-purpose aggregator of every MCP primitive. It must nevertheless bridge any MCP behavior transitively required to complete representative tool workflows, including cancellation, multi-round-trip/input-required exchanges, task continuations, resource follow-ups, and structured or multimedia results.

Downstream Tasks are exposed through Manager-owned opaque task identities and retain the Managed Use Lease needed to service them until completion, failure, cancellation, or expiry. Follow-up resource links are similarly rewritten to Manager-owned authenticated references so a later resource read can be routed back to the exact downstream server and original URI without exposing Server IDs as routing selectors. Embedded resources are preserved unchanged. Standalone prompt/resource aggregation is added before v1 removal only when compatibility testing shows real supported servers require it.