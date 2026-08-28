# Store user routing preferences as a separate overlay

Status: accepted

When semantic enrichment finds materially overlapping tools that cannot be ranked confidently, GPT Tunnel Manager may surface an Ambiguity Review through the connected agent instead of inventing a preference. The review presents the competing tools with relevant pros, cons, and conditional use cases, and the user's answer is persisted as a Routing Preference.

Routing Preferences are separate from authoritative downstream metadata and base Semantic Enrichment. They may be global or belong to named Routing Profiles for project/workflow-specific behavior, may prefer a whole Server Entry or narrower tool sets, and may contain conditional applicability. They survive reindexing while their referenced tool identities and assumptions remain compatible; changed/deleted tools or contradictory newer preferences mark affected preferences for review rather than silently applying stale guidance. Preferences can influence search ranking and selection explanations but never change schemas, annotations, executor classes, authorization, or execution safety.