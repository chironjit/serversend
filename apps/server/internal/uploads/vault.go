package uploads

import (
	"context"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
	"github.com/chironjit/serversend/shared/vault"
)

func (s *Service) Vault(ctx context.Context, account protocol.ID) (vault.Envelope, error) {
	return s.repo.Vault(ctx, account)
}
func (s *Service) PutVault(ctx context.Context, account protocol.ID, e vault.Envelope) error {
	if e.ServerID != s.serverID {
		return api.Error{Code: api.InvalidRequest, Message: "Wrong vault server"}
	}
	return s.repo.PutVault(ctx, account, e)
}
