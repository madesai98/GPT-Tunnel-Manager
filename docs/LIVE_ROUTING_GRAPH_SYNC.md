# Live routing graph synchronization

The Routing workspace is a live operational view. When a running downstream MCP server changes its published `tools/list` contract, the source graph immediately follows the live tool membership instead of remaining frozen on the last active routing generation.

Tools that are present live but are not yet part of the active or staging generation are shown as `NEW · REFRESH INDEX`. They are visible for inspection but are excluded from routing preference drafts until an index refresh incorporates their authoritative contract and produces an assumption fingerprint.

While live/index membership differs, the workspace stays in Sources view because the previous capability hierarchy is stale for the new live contract. Capability view becomes available again after refresh and capability reconciliation.

The desktop sidebar exposes one Routing tab for this combined indexing/routing workflow; the former separate Index navigation entry is removed.
