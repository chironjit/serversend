package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/chironjit/serversend/apps/server/internal/auth"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

func decodeSessionBody(w http.ResponseWriter, r *http.Request, out any) error {
	content, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || content != "application/json" || r.Header.Get("Content-Encoding") != "" {
		return errors.New("send one application/json object")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.MaxAuthenticationBytes))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(out); err != nil {
		return err
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return errors.New("send one application/json object")
	}
	return nil
}
func sameOrigin(w http.ResponseWriter, r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" && origin != "https://"+r.Host || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeError(w, r, 403, api.PermissionDenied, "Use the server's own secure origin")
		return false
	}
	return true
}
func serveDeviceSessions(w http.ResponseWriter, r *http.Request, service *auth.Service) bool {
	if r.URL.Path != api.BasePath+"/sessions/refresh" && r.URL.Path != api.BasePath+"/sessions/revoke" {
		return false
	}
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		writeError(w, r, 405, api.InvalidRequest, "Use POST")
		return true
	}
	if !sameOrigin(w, r) {
		return true
	}
	var request api.RefreshRequest
	if err := decodeSessionBody(w, r, &request); err != nil {
		writeAuthenticationDecodeError(w, r, err)
		return true
	}
	if strings.HasSuffix(r.URL.Path, "/revoke") {
		if err := service.RevokeRefresh(r.Context(), request); err != nil {
			writeAuthenticationError(w, r, err)
		} else {
			w.WriteHeader(204)
		}
		return true
	}
	result, err := service.Refresh(r.Context(), request)
	if err != nil {
		writeAuthenticationError(w, r, err)
	} else {
		writeJSON(w, r, 200, result)
	}
	return true
}
func serveDeviceAdmin(w http.ResponseWriter, r *http.Request, service *auth.Service) bool {
	prefix := api.BasePath + "/admin/users/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, prefix), "/")
	if len(parts) < 2 || protocol.ID(parts[0]).Validate() != nil {
		writeError(w, r, 404, api.NotFound, "Account action not found")
		return true
	}
	target := protocol.ID(parts[0])
	action := parts[1]
	if len(parts) == 2 && action == "devices" && r.Method == "GET" {
		devices, err := service.Devices(r.Context(), bearer(r), target)
		if err != nil {
			writeAuthenticationError(w, r, err)
		} else {
			writeJSON(w, r, 200, devices)
		}
		return true
	}
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		writeError(w, r, 405, api.InvalidRequest, "Use POST")
		return true
	}
	if !sameOrigin(w, r) {
		return true
	}
	var empty struct{}
	if err := decodeSessionBody(w, r, &empty); err != nil {
		writeAuthenticationDecodeError(w, r, err)
		return true
	}
	var err error
	switch {
	case len(parts) == 2 && action == "reset-password":
		var issued auth.IssuedPassword
		issued, err = service.ResetUserPassword(r.Context(), bearer(r), target)
		if err == nil {
			writeJSON(w, r, 200, issued)
			return true
		}
	case len(parts) == 2 && (action == "disable" || action == "enable"):
		err = service.SetUserDisabled(r.Context(), bearer(r), target, action == "disable")
	case len(parts) == 2 && action == "reset-totp":
		err = service.ResetUserTOTP(r.Context(), bearer(r), target)
	case len(parts) == 4 && action == "devices" && parts[3] == "revoke":
		err = service.RevokeDevice(r.Context(), bearer(r), target, protocol.ID(parts[2]))
	default:
		writeError(w, r, 404, api.NotFound, "Account action not found")
		return true
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, r, 404, api.NotFound, "Account or device not found")
	} else if err != nil {
		writeAuthenticationError(w, r, err)
	} else {
		w.WriteHeader(204)
	}
	return true
}

func serveOwnDevices(w http.ResponseWriter, r *http.Request, service *auth.Service) bool {
	prefix := api.BasePath + "/sessions/devices"
	if r.URL.Path != prefix && !strings.HasPrefix(r.URL.Path, prefix+"/") {
		return false
	}
	if r.URL.Path == prefix && r.Method == "GET" {
		devices, err := service.OwnDevices(r.Context(), bearer(r), protocol.ID(r.URL.Query().Get("before")))
		if err != nil {
			writeAuthenticationError(w, r, err)
		} else {
			writeJSON(w, r, 200, devices)
		}
		return true
	}
	if r.Method != "POST" {
		writeError(w, r, 405, api.InvalidRequest, "Use GET to list devices or POST to revoke")
		return true
	}
	if !sameOrigin(w, r) {
		return true
	}
	var empty struct{}
	if err := decodeSessionBody(w, r, &empty); err != nil {
		writeAuthenticationDecodeError(w, r, err)
		return true
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, prefix+"/"), "/")
	if len(parts) != 2 || parts[1] != "revoke" {
		writeError(w, r, 404, api.NotFound, "Device action not found")
		return true
	}
	err := service.RevokeOwnDevice(r.Context(), bearer(r), protocol.ID(parts[0]), parts[0] == "others")
	if err != nil {
		writeAuthenticationError(w, r, err)
	} else {
		w.WriteHeader(204)
	}
	return true
}
