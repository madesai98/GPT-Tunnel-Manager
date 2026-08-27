# Own foreground tunnel runtimes

Tunnel Manager owns `tunnel-client run` processes rather than handing lifecycle supervision to an external service. The application must own the lifetime of every tunnel it starts so all Manager, Always On, Managed, and Manual runtimes stop when Tunnel Manager exits; direct ownership also keeps runtime diagnostics observable by the application.

"Foreground" in this ADR describes lifecycle ownership only. Desktop builds must not create visible terminal or console windows for tunnel-client, managed MCP servers, probes, shutdown helpers, or other background child processes.
