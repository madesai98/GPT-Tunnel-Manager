# Infer Managed idle activity from tunnel-client telemetry

Tunnel Manager will infer meaningful Managed-server activity from structured `tunnel-client` dispatcher telemetry rather than inserting itself as an MCP proxy. This keeps the Manager out of the MCP data path and preserves a lightweight architecture, at the cost of treating the relevant tunnel-client telemetry schema as a compatibility surface that must be validated before promoting updates; if activity detection becomes uncertain, idle shutdown must fail safe by leaving the server running.
