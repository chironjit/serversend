package httpapi

import (
	"encoding/json"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
	"github.com/chironjit/serversend/shared/vault"
	"io"
	"net/http"
)

func (t *transferRoutes) vault(w http.ResponseWriter, r *http.Request, account protocol.ID) error {
	if r.Method == http.MethodGet {
		e, err := t.service.Vault(r.Context(), account)
		if err != nil {
			return err
		}
		writeJSON(w, r, 200, e)
		return nil
	}
	var e vault.Envelope
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	d.DisallowUnknownFields()
	if d.Decode(&e) != nil || d.Decode(new(any)) != io.EOF {
		return api.Error{Code: api.InvalidRequest, Message: "Invalid vault envelope"}
	}
	if err := t.service.PutVault(r.Context(), account, e); err != nil {
		return err
	}
	w.WriteHeader(204)
	return nil
}
