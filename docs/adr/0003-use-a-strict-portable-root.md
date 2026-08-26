# Use a strict Portable Root

Tunnel Manager keeps mutable configuration, data, managed tools, and optional logs under a Portable Root beside the executable, or beside the `.app` bundle on macOS. If that location is not writable, startup fails with a clear error instead of silently falling back to an OS application-data directory, preserving the application's portability contract even though this is less forgiving in protected or read-only locations.
