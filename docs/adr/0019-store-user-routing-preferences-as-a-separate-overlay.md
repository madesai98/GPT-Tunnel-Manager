# Store user routing preferences as a separate overlay

Status: accepted

When semantic enrichment finds materially overlapping tools that cannot be ranked confidently, GPT Tunnel Manager may surface an Ambiguity Review through the connected agent instead of inventing a preference. Ambiguity Reviews are non-blocking: indexing may complete and neutral ranking remains usable while a group is marked `needs_user_feedback`. The review presents the competing tools with source-grounded pros, cons, and conditional use cases, and the user may choose a preference or explicitly keep neutral behavior.

Routing Preferences are separate from authoritative downstream metadata and base Semantic Enrichment. They may be global or belong to named Routing Profiles for project/workflow-specific behavior, may prefer a whole Server Entry or narrower tool sets, and may contain conditional applicability. Agents may explicitly select a Routing Profile when they know the current context; GPT Tunnel Manager does not silently infer project identity when uncertain.

Preferences survive reindexing while their referenced tool identities and assumptions remain compatible. Changed/deleted tools or incompatible assumptions mark affected preferences `needs_review` rather than silently transferring them. Resolution is deterministic: active Routing Profile rules override Global rules, and conditional tool preferences override tool-set preferences, which override server preferences. Conflicting rules at the same scope and specificity require review instead of using newest-wins behavior.

Preferences can influence search ranking and selection explanations but never change schemas, annotations, executor classes, authorization, or execution safety.