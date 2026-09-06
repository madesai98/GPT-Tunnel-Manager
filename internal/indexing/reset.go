package indexing

import "context"

// Clear removes all generated and cached routing-index state. User routing
// profiles/preferences remain intact because they are configuration, not index
// artifacts. The caller receives an empty index and must run Refresh to rebuild
// from the currently configured downstream tools.
func (s *Service) Clear(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.catalog.ClearIndex(ctx)
}
