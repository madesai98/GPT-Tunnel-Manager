# Use a routing state hash for index freshness

Status: accepted

`routing_revision` remains a monotonic ordering and diagnostic value, but it is not the sole correctness proof for index freshness because routing-relevant state spans JSON configuration, OS secret storage, and the index database. GPT Tunnel Manager computes a deterministic Routing State Hash from routing-relevant configuration plus non-secret secret-version metadata and requires the active Index Generation to match it. Cross-store mutations use pending-invalidation/journal state so a crash cannot leave an old generation incorrectly appearing current.