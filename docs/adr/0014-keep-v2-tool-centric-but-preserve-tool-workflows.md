# Keep v2 tool-centric but preserve complete tool workflows

Status: accepted

GPT Tunnel Manager v2 is primarily a tool router rather than a general-purpose aggregator of every MCP primitive. It must nevertheless bridge any MCP behavior transitively required to complete representative tool workflows, including cancellation, multi-round-trip/input-required exchanges, task continuations, resource follow-ups, and structured or multimedia results. Standalone prompt/resource aggregation is added before v1 removal only when compatibility testing shows real supported servers require it.