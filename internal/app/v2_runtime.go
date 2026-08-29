package app

// Done is closed when the v2 application is asked to shut down.
func (a *V2App) Done() <-chan struct{} {
	if a == nil || a.ctx == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return a.ctx.Done()
}

// RequestShutdown is safe to call from tray/window/signal handlers.
func (a *V2App) RequestShutdown() {
	if a != nil && a.cancel != nil {
		a.cancel()
	}
}
