package httpapi

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/chironjit/serversend/apps/server/internal/auth"
	"github.com/chironjit/serversend/apps/server/internal/uploads"
	"github.com/chironjit/serversend/shared/api"
)

var cpuSample struct {
	sync.Mutex
	total, idle uint64
}

// Linux proc values describe the host visible inside the container, not its cgroup limit.
func hostStats() (cpu *float64, used, total *uint64) {
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		var memory, available uint64
		for _, line := range strings.Split(string(data), "\n") {
			f := strings.Fields(line)
			if len(f) < 2 {
				continue
			}
			n, _ := strconv.ParseUint(f[1], 10, 64)
			if f[0] == "MemTotal:" {
				memory = n * 1024
			}
			if f[0] == "MemAvailable:" {
				available = n * 1024
			}
		}
		if memory > 0 && available <= memory {
			u := memory - available
			used = &u
			total = &memory
		}
	}
	if data, err := os.ReadFile("/proc/stat"); err == nil {
		f := strings.Fields(strings.SplitN(string(data), "\n", 2)[0])
		if len(f) < 9 {
			return
		}
		var sum, idle uint64
		// guest values are already included in user/nice: sum only the first eight counters.
		for i := 1; i <= 8; i++ {
			n, err := strconv.ParseUint(f[i], 10, 64)
			if err != nil {
				return
			}
			sum += n
			if i == 4 || i == 5 {
				idle += n
			}
		}
		cpuSample.Lock()
		defer cpuSample.Unlock()
		if cpuSample.total > 0 && sum > cpuSample.total && idle >= cpuSample.idle {
			delta := sum - cpuSample.total
			inactive := idle - cpuSample.idle
			if inactive <= delta {
				value := 100 * float64(delta-inactive) / float64(delta)
				cpu = &value
			}
		}
		cpuSample.total, cpuSample.idle = sum, idle
	}
	return
}
func serveStats(w http.ResponseWriter, r *http.Request, accounts *auth.Service, transfers *uploads.Service) bool {
	if r.URL.Path != api.BasePath+"/admin/stats" {
		return false
	}
	session, err := accounts.Authorize(r.Context(), bearer(r))
	if err != nil {
		writeAuthenticationError(w, r, err)
		return true
	}
	if session.Role != "admin" {
		writeAuthenticationError(w, r, auth.ErrForbidden)
		return true
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeError(w, r, 405, api.InvalidRequest, "Use GET")
		return true
	}
	var stored int64
	cpu, used, total := hostStats()
	var available *int64
	if transfers != nil {
		if n, err := transfers.Available(); err == nil {
			available = &n
		}
	}
	var storageStats *api.StorageStatistics
	if transfers != nil {
		value, err := transfers.Statistics(r.Context(), "")
		if err != nil {
			writeFailure(w, r, err)
			return true
		}
		storageStats = &value
		stored = value.LogicalBytes
	}
	writeJSON(w, r, 200, struct {
		StorageDetails *api.StorageStatistics `json:"storage"`
		Status         string                 `json:"status"`
		CPU            *float64               `json:"cpu_percent"`
		MemoryUsed     *uint64                `json:"memory_used_bytes"`
		MemoryTotal    *uint64                `json:"memory_total_bytes"`
		Storage        int64                  `json:"storage_bytes"`
		Available      *int64                 `json:"storage_available_bytes"`
	}{storageStats, "Operational", cpu, used, total, stored, available})
	return true
}
