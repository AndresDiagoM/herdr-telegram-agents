package testkit

import (
	"context"
	"sync"

	"github.com/permgps/herdr-telegram-agents/internal/domain"
)

// MemSharingStore returns independent policy snapshots and supports failure
// injection without timestamps from the wall clock.
type MemSharingStore struct {
	mu    sync.Mutex
	state domain.SharingState
	err   error
}

func NewMemSharingStore() *MemSharingStore { return &MemSharingStore{state: domain.NewSharingState()} }
func (s *MemSharingStore) Fail(err error)  { s.mu.Lock(); defer s.mu.Unlock(); s.err = err }
func (s *MemSharingStore) Load(ctx context.Context) (domain.SharingState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.SharingState{}, err
	}
	if s.err != nil {
		return domain.SharingState{}, s.err
	}
	return s.state.Clone(), nil
}
func (s *MemSharingStore) Save(ctx context.Context, st domain.SharingState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.err != nil {
		return s.err
	}
	if err := st.Validate(); err != nil {
		return err
	}
	s.state = st.Clone()
	return nil
}
