package app

import "context"

// IndexClear removes every generated/cached semantic routing-index artifact
// while preserving server configuration and routing preferences.
func (a *V2App) IndexClear(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return a.indexing.Clear(ctx)
}
