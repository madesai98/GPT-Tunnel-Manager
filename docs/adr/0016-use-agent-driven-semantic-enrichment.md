# Use agent-driven semantic enrichment

Status: accepted

GPT Tunnel Manager does not invoke a hidden LLM internally to enrich its Tool Catalog. Instead, the connected MCP agent performs bounded structured enrichment through the Manager MCP indexing workflow, while GPT Tunnel Manager validates, persists, embeds, and dependency-tracks that derived guidance; this keeps model reasoning visible to the connected agent and avoids adding a second general-purpose LLM credential/execution path inside the desktop application.

Enrichment is two-stage. Tool-level semantic enrichment is produced first, then bounded Capability Reconciliation batches normalize overlapping or near-synonymous capability paths across independently enriched tool batches. Taxonomy reconciliation is automatic when it is only naming/organization cleanup. If an ambiguity materially changes which tool should be preferred for a user intent, it becomes a non-blocking Ambiguity Review handled through the separate Routing Preference layer rather than being invented by the enrichment model.