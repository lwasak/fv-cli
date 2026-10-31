package cli

import "github.com/lwasak/fv-cli/internal/store"

// store opens the invoice archive under the resolved data directory, using the session clock.
func (s *session) store(rc *runContext) *store.Store {
	st := store.Open(rc.Paths.DataDir, s.deps.now)
	if s.deps.Ctx != nil {
		st = st.WithContext(s.deps.Ctx)
	}
	return st
}
