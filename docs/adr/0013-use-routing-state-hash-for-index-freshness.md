# Use a routing state hash for index freshness

Status: accepted

`routing_revision` remains a monotonic ordering and diagnostic value, but it is not the sole correctness proof for index freshness because routing-relevant state spans JSON configuration, OS secret storage, and the index database. GPT Tunnel Manager computes a deterministic Routing State Hash from routing-relevant configuration plus routing-relevant resolved-secret fingerprints and requires the active Index Generation to match it. Secret fingerprints are derived with an installation-scoped HMAC key so raw values and reusable unkeyed hashes are never persisted. Cross-store mutations use pending-invalidation/journal state so a crash cannot leave an old generation incorrectly appearing current.

Routine OAuth access/refresh-token rotation, embedding-provider credentials, and the Manager Tunnel Runtime API key are excluded unless they change the downstream routing contract. Explicit downstream reconnect/logout/account or scope changes and replacement of routing-relevant static credentials invalidate the affected routing state.