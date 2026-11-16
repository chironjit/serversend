package httpapi

import (
	"encoding/json"
	"github.com/chironjit/serversend/apps/server/internal/auth"
	"github.com/chironjit/serversend/shared/api"
	"io"
	"net/http"
)

func serveUsers(w http.ResponseWriter, r *http.Request, service *auth.Service) bool {
	if r.URL.Path != api.BasePath+"/admin/users" {
		return false
	}
	if r.Method != "POST" {
		writeError(w, r, 405, api.InvalidRequest, "Use POST")
		return true
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != "https://"+r.Host || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeError(w, r, 403, api.PermissionDenied, "Use the server's own secure origin")
		return true
	}
	var request struct {
		Username string `json:"username"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.MaxAuthenticationBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeAuthenticationDecodeError(w, r, err)
		return true
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeError(w, r, 400, api.InvalidRequest, "Send one JSON object")
		return true
	}
	issued, err := service.CreateUserFromSession(r.Context(), bearer(r), request.Username)
	if err != nil {
		writeAuthenticationError(w, r, err)
	} else {
		writeJSON(w, r, 201, issued)
	}
	return true
}
