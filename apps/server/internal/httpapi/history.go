package httpapi

import (
	"encoding/json"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
	"net/http"
	"strconv"
)

func historyPage(r *http.Request) api.PageRequest {
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		limit, _ = strconv.Atoi(v)
	}
	return api.PageRequest{Limit: limit, Cursor: api.Cursor(r.URL.Query().Get("cursor"))}
}
func (t *transferRoutes) history(w http.ResponseWriter, r *http.Request, a protocol.ID) error {
	p, e := t.service.History(r.Context(), a, protocol.ID(r.PathValue("file")), historyPage(r))
	if e == nil {
		writeJSON(w, r, 200, p)
	}
	return e
}
func (t *transferRoutes) trash(w http.ResponseWriter, r *http.Request, a protocol.ID) error {
	p, e := t.service.TrashPage(r.Context(), a, historyPage(r))
	if e == nil {
		writeJSON(w, r, 200, p)
	}
	return e
}
func (t *transferRoutes) restoreTrash(w http.ResponseWriter, r *http.Request, a protocol.ID) error {
	e := t.service.RestoreTrash(r.Context(), a, protocol.ID(r.PathValue("entry")))
	if e == nil {
		w.WriteHeader(204)
	}
	return e
}
func (t *transferRoutes) purgeTrash(w http.ResponseWriter, r *http.Request, a protocol.ID) error {
	if r.URL.Query().Get("confirm") != "purge" {
		return api.Error{Code: api.InvalidRequest, Message: "Explicit purge confirmation required"}
	}
	e := t.service.PurgeTrash(r.Context(), a, protocol.ID(r.PathValue("entry")))
	if e == nil {
		w.WriteHeader(204)
	}
	return e
}
func (t *transferRoutes) selectVersion(w http.ResponseWriter, r *http.Request, a protocol.ID) error {
	var body struct {
		Expected protocol.Revision `json:"expected_head"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, api.MaxControlBytes)).Decode(&body) != nil {
		return api.Error{Code: api.InvalidRequest, Message: "Expected revision required"}
	}
	e := t.service.SelectVersion(r.Context(), a, protocol.ID(r.PathValue("file")), protocol.ID(r.PathValue("version")), body.Expected)
	if e == nil {
		w.WriteHeader(204)
	}
	return e
}

func (t *transferRoutes) uploadTarget(w http.ResponseWriter, r *http.Request, a protocol.ID) error {
	e, err := t.service.UploadTarget(r.Context(), a, protocol.ID(r.URL.Query().Get("parent_id")), r.URL.Query().Get("name"))
	if err == nil {
		writeJSON(w, r, 200, struct {
			Entry *api.CatalogEntry `json:"entry"`
		}{e})
	}
	return err
}
