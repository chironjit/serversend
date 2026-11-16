package httpapi

import (
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/chironjit/serversend/apps/server/internal/auth"
	"github.com/chironjit/serversend/apps/server/internal/uploads"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

// WithTransfers enables device sessions and the standard upload, catalog and
// download routes. It requires WithAuthentication: every route below resolves
// its caller from an access token on each request.
func WithTransfers(service *uploads.Service) Option {
	return func(o *options) { o.transfers = service }
}

const (
	sessionsPath       = api.BasePath + "/sessions"
	currentSessionPath = api.BasePath + "/sessions/current"
)

func bearer(r *http.Request) string {
	parts := strings.Split(r.Header.Get("Authorization"), " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		return ""
	}
	return parts[1]
}

// writeFailure sends typed API errors as they are and hides everything else
// behind a generic message; the detail goes to the operator's log only.
func writeFailure(w http.ResponseWriter, r *http.Request, err error) {
	var typed api.Error
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &typed):
		writeJSON(w, r, typed.Code.HTTPStatus(), api.ErrorResponse{Error: typed})
	case errors.As(err, &tooLarge):
		writeError(w, r, 413, api.RequestTooLarge, "Request body exceeds the server's advertised limit")
	case r.Context().Err() != nil:
		writeError(w, r, 503, api.Unavailable, "Request was interrupted; try again")
	default:
		log.Printf("request %s %s failed: %v", r.Method, r.URL.Path, err)
		writeError(w, r, 500, api.InternalError, "The server could not complete this request; try again or contact the server operator")
	}
}

// serveSessions turns a verified second factor into a device session, and
// ends one. Sign-in uses the restricted challenge, never an existing session.
func serveSessions(w http.ResponseWriter, r *http.Request, accounts *auth.Service) bool {
	switch {
	case r.URL.Path == sessionsPath && r.Method == http.MethodPost:
		contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || contentType != "application/json" || r.Header.Get("Content-Encoding") != "" {
			writeError(w, r, 415, api.InvalidRequest, "Send an uncompressed application/json request")
			return true
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != "https://"+r.Host || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			writeError(w, r, 403, api.PermissionDenied, "Use the server's own secure origin for this request")
			return true
		}
		r.Body = http.MaxBytesReader(w, r.Body, api.MaxAuthenticationBytes)
		request, err := api.DecodeSessionRequest(r.Body)
		if err != nil {
			writeAuthenticationDecodeError(w, r, err)
			return true
		}
		session, err := accounts.CreateSession(r.Context(), bearer(r), request.Code, request.DeviceName)
		if err != nil {
			writeAuthenticationError(w, r, err)
			return true
		}
		writeJSON(w, r, 201, session)
	case r.URL.Path == currentSessionPath && r.Method == http.MethodDelete:
		if err := accounts.EndSession(r.Context(), bearer(r)); err != nil {
			writeFailure(w, r, err)
			return true
		}
		w.WriteHeader(http.StatusNoContent)
	case r.URL.Path == sessionsPath || r.URL.Path == currentSessionPath:
		w.Header().Set("Allow", map[string]string{sessionsPath: "POST", currentSessionPath: "DELETE"}[r.URL.Path])
		writeError(w, r, 405, api.InvalidRequest, "Method not allowed for this endpoint")
	default:
		return false
	}
	return true
}

type transferRoutes struct {
	accounts *auth.Service
	service  *uploads.Service
	mux      *http.ServeMux
}

type accountHandler func(w http.ResponseWriter, r *http.Request, account protocol.ID) error

func newTransferRoutes(accounts *auth.Service, service *uploads.Service) *transferRoutes {
	t := &transferRoutes{accounts: accounts, service: service, mux: http.NewServeMux()}
	for pattern, handler := range map[string]accountHandler{
		"POST " + api.BasePath + "/uploads":                                       t.create,
		"GET " + api.BasePath + "/uploads/{upload}":                               t.get,
		"GET " + api.BasePath + "/uploads/{upload}/missing":                       t.missing,
		"PUT " + api.BasePath + "/uploads/{upload}/chunks/{index}":                t.putChunk,
		"POST " + api.BasePath + "/uploads/{upload}/finalize":                     t.finalize,
		"GET " + api.BasePath + "/catalog":                                        t.catalog,
		"GET " + api.BasePath + "/files/{file}/versions/{version}/manifest":       t.manifest,
		"GET " + api.BasePath + "/files/{file}/versions/{version}/chunks/{index}": t.chunk,
	} {
		t.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			// Authorization is resolved per request from live account state.
			session, err := accounts.Authorize(r.Context(), bearer(r))
			if errors.Is(err, auth.ErrCredentials) {
				writeError(w, r, 401, api.AuthenticationRequired, "Sign in again to continue")
				return
			}
			if err == nil {
				err = handler(w, r, session.AccountID)
			}
			if err != nil {
				writeFailure(w, r, err)
			}
		})
	}
	return t
}

// serve handles only the transfer namespaces; unknown paths inside them get
// typed errors instead of the mux's plain-text defaults.
func (t *transferRoutes) serve(w http.ResponseWriter, r *http.Request) bool {
	inside := false
	for _, prefix := range []string{"/uploads", "/catalog", "/files"} {
		inside = inside || r.URL.Path == api.BasePath+prefix || strings.HasPrefix(r.URL.Path, api.BasePath+prefix+"/")
	}
	if !inside {
		return false
	}
	if _, pattern := t.mux.Handler(r); pattern == "" {
		writeError(w, r, 404, api.NotFound, "Endpoint not found or method not allowed")
		return true
	}
	t.mux.ServeHTTP(w, r)
	return true
}

func pathIndex(r *http.Request) (int64, error) {
	value := r.PathValue("index")
	index, err := strconv.ParseInt(value, 10, 64)
	if err != nil || index < 0 || strconv.FormatInt(index, 10) != value {
		return 0, api.Error{Code: api.InvalidRequest, Message: "Chunk index must be a canonical non-negative integer"}
	}
	return index, nil
}

func (t *transferRoutes) create(w http.ResponseWriter, r *http.Request, account protocol.ID) error {
	r.Body = http.MaxBytesReader(w, r.Body, api.MaxAuthenticationBytes)
	request, err := api.DecodeCreateUpload(r.Body, t.service.Limits())
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return err
		}
		return api.Error{Code: api.InvalidRequest, Message: "Send a valid upload request within the server's advertised limits"}
	}
	upload, created, err := t.service.Create(r.Context(), account, r.Header.Get(api.IdempotencyKeyHeader), request)
	if err != nil {
		return err
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, r, status, upload)
	return nil
}

func (t *transferRoutes) get(w http.ResponseWriter, r *http.Request, account protocol.ID) error {
	upload, err := t.service.Get(r.Context(), account, protocol.ID(r.PathValue("upload")))
	if err == nil {
		writeJSON(w, r, 200, upload)
	}
	return err
}

func (t *transferRoutes) missing(w http.ResponseWriter, r *http.Request, account protocol.ID) error {
	missing, err := t.service.Missing(r.Context(), account, protocol.ID(r.PathValue("upload")))
	if err == nil {
		writeJSON(w, r, 200, missing)
	}
	return err
}

func (t *transferRoutes) putChunk(w http.ResponseWriter, r *http.Request, account protocol.ID) error {
	index, err := pathIndex(r)
	if err != nil {
		return err
	}
	if r.ContentLength <= 0 || r.Header.Get("Content-Encoding") != "" {
		return api.Error{Code: api.InvalidRequest, Message: "Send the exact uncompressed chunk bytes with a Content-Length"}
	}
	if r.ContentLength > t.service.Limits().MaxChunkBytes {
		return api.Error{Code: api.RequestTooLarge, Message: "Chunk exceeds the server's advertised maximum size"}
	}
	r.Body = http.MaxBytesReader(w, r.Body, r.ContentLength)
	acknowledgement, created, err := t.service.PutChunk(r.Context(), account, protocol.ID(r.PathValue("upload")), index, r.ContentLength, protocol.Digest(r.Header.Get(api.ChunkDigestHeader)), r.Body)
	if err != nil {
		return err
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, r, status, acknowledgement)
	return nil
}

func (t *transferRoutes) finalize(w http.ResponseWriter, r *http.Request, account protocol.ID) error {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, t.service.Limits().MaxManifestBytes))
	if err != nil {
		return err
	}
	result, err := t.service.Finalize(r.Context(), account, protocol.ID(r.PathValue("upload")), body)
	if err == nil {
		writeJSON(w, r, 200, result)
	}
	return err
}

func (t *transferRoutes) catalog(w http.ResponseWriter, r *http.Request, account protocol.ID) error {
	page, err := t.service.Catalog(r.Context(), account)
	if err == nil {
		writeJSON(w, r, 200, page)
	}
	return err
}

func (t *transferRoutes) manifest(w http.ResponseWriter, r *http.Request, account protocol.ID) error {
	manifest, err := t.service.Manifest(r.Context(), account, protocol.ID(r.PathValue("file")), protocol.ID(r.PathValue("version")))
	if err != nil {
		return err
	}
	// The exact stored bytes are returned, never a re-encoding.
	w.Header().Set("Content-Length", strconv.Itoa(len(manifest)))
	w.WriteHeader(200)
	_, _ = w.Write(manifest)
	return nil
}

func (t *transferRoutes) chunk(w http.ResponseWriter, r *http.Request, account protocol.ID) error {
	index, err := pathIndex(r)
	if err != nil {
		return err
	}
	object, chunk, err := t.service.OpenChunk(r.Context(), account, protocol.ID(r.PathValue("file")), protocol.ID(r.PathValue("version")), index)
	if err != nil {
		return err
	}
	defer object.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(chunk.Bytes, 10))
	w.Header().Set(api.ChunkDigestHeader, string(chunk.SHA256))
	w.WriteHeader(200)
	// The client verifies the digest; a short copy surfaces there as a mismatch.
	_, _ = io.Copy(w, io.LimitReader(object, chunk.Bytes))
	return nil
}
