# Fail closed during Lifecycle Skill preflight

Before ChatGPT uses any Participating Developer Plugin, the Lifecycle Skill must resolve its `GTM_SERVER_ID` through the Manager MCP and obey the Server Entry's current mode and state. Always On entries may only be observed and waited on; Manual entries may be used only when already Ready; Managed entries may use the Manager MCP lifecycle mutations. The Manager MCP's `get_status(wait_for_ready=true)` waits 30 seconds by default and accepts at most 60 seconds per call.

If the Manager Developer Plugin cannot be reached, the Skill fails closed: it tells the user that GPT Tunnel Manager must be started, does not invoke the target Developer Plugin, performs no unrelated continuation work, and ends that assistant response so the user can start Tunnel Manager before continuing.
