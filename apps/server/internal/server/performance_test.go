package server

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chironjit/serversend/apps/server/internal/httpapi"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/client"
)

// BenchmarkStandardUpload uses the production authorization, SQLite, durable
// object writes and finalization paths. Setup/onboarding are outside the timer.
// Each operation publishes a distinct new file/version. This is loopback data,
// not a deployment, fairness, resume, private-mode or large-catalog qualification.
func BenchmarkStandardUpload(b *testing.B) {
	for _, transport := range []string{"http2", "http3"} {
		for _, size := range []int{1024, 8 << 20} {
			for _, chunk := range []int64{1 << 20, 4 << 20, 8 << 20} {
				if size == 1024 && chunk != 4<<20 {
					continue
				}
				b.Run(fmt.Sprintf("%s/bytes_%d/chunk_%d", transport, size, chunk), func(b *testing.B) {
					benchmarkUpload(b, transport, size, chunk)
				})
			}
		}
	}
}

func benchmarkUpload(b *testing.B, transport string, size int, chunk int64) {
	cfg := fixture(b)
	s, err := New(b.Context(), cfg)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	apiHandler := httpapi.New(s.ready, httpapi.WithAuthentication(s.accounts), httpapi.WithTransfers(s.transfers), httpapi.WithHTTP3())
	var mu sync.Mutex
	durations := map[string]time.Duration{}
	counts := map[string]int{}
	origin := startDualServer(b, s, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == api.BasePath+"/capabilities" {
			capabilities := httpapi.Capabilities()
			capabilities.Transports = append(capabilities.Transports, api.HTTP3)
			capabilities.Limits.MinChunkBytes = chunk
			capabilities.Limits.MaxChunkBytes = chunk
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(capabilities)
			return
		}
		stage := "other"
		switch {
		case r.URL.Path == api.BasePath+"/uploads":
			stage = "create"
		case strings.Contains(r.URL.Path, "/chunks/"):
			stage = "chunk"
		case strings.HasSuffix(r.URL.Path, "/finalize"):
			stage = "finalize"
		}
		if stage != "other" {
			expected := 2
			if transport == "http3" {
				expected = 3
			}
			if r.ProtoMajor != expected {
				b.Errorf("%s measurement used %s", transport, r.Proto)
			}
		}
		start := time.Now()
		apiHandler.ServeHTTP(w, r)
		mu.Lock()
		durations[stage] += time.Since(start)
		counts[stage]++
		mu.Unlock()
	}))
	issued, err := s.Accounts().BootstrapAdmin(b.Context(), "bench")
	if err != nil {
		b.Fatal(err)
	}
	c, _ := onboardedTransportClient(b, origin, strings.ReplaceAll(s.CertificateFingerprint(), " ", ""), "bench", issued.TemporaryPassword, client.TransportOptions{PreferHTTP3: transport == "http3"})
	if _, err := s.db.Exec(`UPDATE accounts SET role='user' WHERE username='bench'`); err != nil {
		b.Fatal(err)
	}
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		b.Fatal(err)
	}
	sourceRoot, err := filepath.EvalSymlinks(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	path := filepath.Join(sourceRoot, "source.bin")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		b.Fatal(err)
	}
	if _, err := c.Upload(b.Context(), path, nil); err != nil {
		b.Fatal(err)
	}
	mu.Lock()
	durations = map[string]time.Duration{}
	counts = map[string]int{}
	mu.Unlock()
	b.ReportAllocs()
	b.SetBytes(int64(size))
	b.ResetTimer()
	for range b.N {
		if _, err := c.Upload(b.Context(), path, nil); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	mu.Lock()
	defer mu.Unlock()
	for _, stage := range []string{"create", "chunk", "finalize"} {
		b.ReportMetric(float64(durations[stage].Nanoseconds())/float64(b.N), stage+"_ns/file")
		b.ReportMetric(float64(counts[stage])/float64(b.N), stage+"_requests/file")
	}
}
