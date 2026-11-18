package uploads

import (
	"context"
	"github.com/chironjit/serversend/shared/protocol"
)

func (s *Service) maintain(ctx context.Context) error {
	if err := s.applyJournal(ctx); err != nil {
		return err
	}
	if err := s.repo.RetireUploads(ctx, s.now(), "", ""); err != nil {
		return err
	}
	if s.readers == 0 {
		if err := s.repo.Collect(ctx, s.now()); err != nil {
			return err
		}
	}
	return s.applyJournal(ctx)
}

// Maintain shares the mutation lock with writes, finalization and cancellation:
// cleanup cannot remove bytes belonging to an in-flight chunk publication.
func (s *Service) Maintain(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maintain(ctx)
}

func (s *Service) Cancel(ctx context.Context, account, id protocol.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.transfer(ctx, account, id)
	if err != nil {
		return err
	}
	if t.State == protocol.UploadExpired || (t.State == protocol.UploadOpen && !t.ExpiresAt.After(s.now())) {
		return errGone
	}
	if err := s.repo.RetireUploads(ctx, s.now(), account, id); err != nil {
		return err
	}
	return s.applyJournal(ctx)
}
