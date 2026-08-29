# Require atomic semantic index generations

Status: accepted

Normal routed operations require a committed semantic Index Generation containing authoritative catalog data, lexical retrieval artifacts, embeddings, and bounded agent-produced Semantic Enrichment. Rebuilds occur in staging, reuse unchanged content-addressed artifacts, and become visible only through an atomic commit; stale or superseded routing state fails closed. Agent-driven enrichment is deliberate because GPT Tunnel Manager does not assume access to the host model as a hidden internal subroutine.