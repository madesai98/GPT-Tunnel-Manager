# Own foreground tunnel runtimes

Tunnel Manager uses foreground `tunnel-client run` processes rather than detached runtime supervision. The application must own the lifetime of every tunnel it starts so all Manager, Always On, Managed, and Manual runtimes stop when Tunnel Manager exits; foreground ownership also keeps runtime diagnostics directly observable by the application.
