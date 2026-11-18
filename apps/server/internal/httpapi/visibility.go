package httpapi

import (
	"encoding/json"
	"fmt"
	"github.com/chironjit/serversend/apps/server/internal/auth"
	"github.com/chironjit/serversend/apps/server/internal/uploads"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
	"net/http"
	"strconv"
	"time"
)

// Periodic redacted hints use a bounded, deterministic replay window. They
// carry no account activity, names, capacity, credentials or private metadata.
// No transfer ever waits for a subscriber or allocates a subscriber queue.
func visibility(accounts *auth.Service, transfers *uploads.Service) func(http.ResponseWriter, *http.Request) bool {
	epoch := time.Now().UnixMilli()
	started := time.Now()
	slots := make(chan struct{}, 128)
	sequence := func() int64 { return epoch + int64(time.Since(started)/(2*time.Second)) }
	return func(w http.ResponseWriter, r *http.Request) bool {
		path := r.URL.Path
		admin := path == api.BasePath+"/admin/statistics" || path == api.BasePath+"/admin/events"
		stream := path == api.BasePath+"/events" || path == api.BasePath+"/admin/events"
		if !admin && !stream && path != api.BasePath+"/statistics" {
			return false
		}
		session, err := accounts.Authorize(r.Context(), bearer(r))
		if err != nil {
			writeAuthenticationError(w, r, err)
			return true
		}
		if (admin && session.Role != "admin") || (!admin && session.Role != "user") {
			writeAuthenticationError(w, r, auth.ErrForbidden)
			return true
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, r, 405, api.InvalidRequest, "Use GET")
			return true
		}
		if !stream {
			scope := session.AccountID
			if admin {
				scope = protocol.ID(r.URL.Query().Get("account_id"))
				if scope != "" && scope.Validate() != nil {
					writeError(w, r, 400, api.InvalidRequest, "Invalid account ID")
					return true
				}
			}
			stats, err := transfers.Statistics(r.Context(), scope)
			if err != nil {
				writeFailure(w, r, err)
			} else {
				writeJSON(w, r, 200, stats)
			}
			return true
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			writeError(w, r, 503, api.Unavailable, "Too many event streams; retry shortly")
			return true
		}
		next := sequence()
		if cursor := r.Header.Get("Last-Event-ID"); cursor != "" {
			n, e := strconv.ParseInt(cursor, 10, 64)
			if e != nil || strconv.FormatInt(n, 10) != cursor || n < 0 {
				writeError(w, r, 400, api.InvalidRequest, "Invalid event cursor")
				return true
			}
			if n < epoch || n > sequence() || sequence()-n > 60 {
				writeError(w, r, api.SnapshotExpired.HTTPStatus(), api.SnapshotExpired, "Event history expired; reconcile statistics and catalog")
				return true
			}
			next = n + 1
		}
		controller := http.NewResponseController(w)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Accel-Buffering", "no")
		if controller.SetWriteDeadline(time.Now().Add(5*time.Second)) != nil {
			return true
		}
		defer controller.SetWriteDeadline(time.Time{})
		w.WriteHeader(200)
		if controller.Flush() != nil {
			return true
		}
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			// Recheck authorization on every delivery, including replay.
			if _, err := accounts.Authorize(r.Context(), bearer(r)); err != nil {
				return true
			}
			for next <= sequence() {
				event := api.Event{Sequence: protocol.Revision(strconv.FormatInt(next, 10)), Type: api.StatisticsChanged}
				data, _ := json.Marshal(event)
				_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if _, err := fmt.Fprintf(w, "id: %d\nevent: statistics_changed\ndata: %s\n\n", next, data); err != nil {
					return true
				}
				if controller.Flush() != nil {
					return true
				}
				next++
			}
			select {
			case <-r.Context().Done():
				return true
			case <-ticker.C:
			}
		}
	}
}
