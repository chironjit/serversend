package client

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var benchmarkDigest [32]byte

func BenchmarkLocalStages(b *testing.B) {
	for _, size := range []int{1 << 20, 4 << 20, 8 << 20} {
		b.Run(fmt.Sprintf("sha256/bytes_%d", size), func(b *testing.B) {
			data := bytes.Repeat([]byte{0x7d}, size)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				benchmarkDigest = sha256.Sum256(data)
			}
		})
	}
	b.Run("scan_1024_entries", func(b *testing.B) {
		dir := b.TempDir()
		for i := range 1024 {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file-%04d", i)), nil, 0o600); err != nil {
				b.Fatal(err)
			}
		}
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			f, err := os.Open(dir)
			if err != nil {
				b.Fatal(err)
			}
			count := 0
			for {
				entries, err := f.ReadDir(128)
				count += len(entries)
				if err == io.EOF {
					break
				}
				if err != nil {
					f.Close()
					b.Fatal(err)
				}
			}
			f.Close()
			if count != 1024 {
				b.Fatal("incomplete scan")
			}
		}
		b.StopTimer()
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*1024), "ns/entry")
	})
}

// This disposable rig isolates metadata request/TLS/JSON overhead. Batching
// is a measurement candidate, not a production API or a durability result.
func BenchmarkMetadataRequests(b *testing.B) {
	type record struct {
		Name  string `json:"name"`
		Bytes int64  `json:"bytes"`
	}
	for _, batch := range []int{1, 128} {
		for _, reuse := range []bool{false, true} {
			b.Run(fmt.Sprintf("records_%d/reuse_%t", batch, reuse), func(b *testing.B) {
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.ProtoMajor != 2 {
						b.Error("request did not negotiate HTTP2")
					}
					var records []record
					if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&records); err != nil || len(records) != batch {
						http.Error(w, "invalid fixture", 400)
						return
					}
					w.WriteHeader(http.StatusNoContent)
				}))
				server.EnableHTTP2 = true
				server.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
				server.StartTLS()
				defer server.Close()
				c := server.Client()
				c.Timeout = 5 * time.Second
				defer c.CloseIdleConnections()
				records := make([]record, batch)
				for i := range records {
					records[i] = record{Name: fmt.Sprintf("file-%08d", i), Bytes: 1024}
				}
				body, err := json.Marshal(records)
				if err != nil {
					b.Fatal(err)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					response, err := c.Post(server.URL, "application/json", bytes.NewReader(body))
					if err != nil {
						b.Fatal(err)
					}
					_, err = io.Copy(io.Discard, response.Body)
					response.Body.Close()
					if err != nil || response.StatusCode != 204 {
						b.Fatalf("response: %d %v", response.StatusCode, err)
					}
					if !reuse {
						c.CloseIdleConnections()
					}
				}
				b.StopTimer()
				b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*batch), "ns/record")
				b.ReportMetric(1/float64(batch), "requests/record")
			})
		}
	}
}
