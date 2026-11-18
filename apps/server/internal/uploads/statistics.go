package uploads

import (
	"context"
	"database/sql"
	"errors"
	"github.com/chironjit/serversend/apps/server/internal/layout"
	"github.com/chironjit/serversend/apps/server/internal/storage"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
	"io"
	"sync"
	"time"
)

type trafficSample struct {
	at                  time.Time
	wire, useful, retry int64
}
type trafficCounters struct {
	sync.Mutex
	accounts map[protocol.ID][]trafficSample
}

// RecordTraffic uses fixed one-second buckets and a ten-second rolling window.
// Only authenticated accounts can allocate a counter; no paths are recorded.
func (s *Service) RecordTraffic(account protocol.ID, wire, useful, retry int64) {
	s.traffic.Lock()
	defer s.traffic.Unlock()
	if s.traffic.accounts == nil {
		s.traffic.accounts = map[protocol.ID][]trafficSample{}
	}
	now := s.now().UTC().Truncate(time.Second)
	samples := s.traffic.accounts[account]
	for len(samples) > 0 && now.Sub(samples[0].at) >= 10*time.Second {
		samples = samples[1:]
	}
	if len(samples) == 0 || !samples[len(samples)-1].at.Equal(now) {
		samples = append(samples, trafficSample{at: now})
	}
	p := &samples[len(samples)-1]
	p.wire += wire
	p.useful += useful
	p.retry += retry
	if len(samples) > 10 {
		samples = samples[len(samples)-10:]
	}
	s.traffic.accounts[account] = samples
}
func (s *Service) trafficStats(account protocol.ID) (t api.TransferStatistics) {
	s.traffic.Lock()
	defer s.traffic.Unlock()
	now := s.now()
	t.UpdatedAt = now.UTC()
	for id, samples := range s.traffic.accounts {
		if len(samples) == 0 || now.Sub(samples[len(samples)-1].at) >= 10*time.Second {
			delete(s.traffic.accounts, id)
			continue
		}
		if account != "" && account != id {
			continue
		}
		for _, p := range samples {
			if now.Sub(p.at) < 10*time.Second {
				t.WireBytes += p.wire
				t.UsefulBytes += p.useful
				t.RetryBytes += p.retry
			}
		}
	}
	t.WireRate = float64(t.WireBytes) / 10
	t.UsefulRate = float64(t.UsefulBytes) / 10
	t.RetryRate = float64(t.RetryBytes) / 10
	return
}

// SetCapacityPaths is called once during composition before serving requests.
func (s *Service) SetCapacityPaths(paths map[string]string) { s.capacityPaths = paths }

func (s *Service) Statistics(ctx context.Context, account protocol.ID) (api.StorageStatistics, error) {
	select {
	case s.statsGate <- struct{}{}:
	case <-ctx.Done():
		return api.StorageStatistics{}, ctx.Err()
	}
	defer func() { <-s.statsGate }()
	if cached, ok := s.statsCache[account]; ok && s.now().Sub(cached.UpdatedAt) < 5*time.Second {
		cached.Transfer = s.trafficStats(account)
		return cached, nil
	}
	result, err := s.measureStatistics(ctx, account)
	if err == nil {
		if len(s.statsCache) >= 200 {
			for key := range s.statsCache {
				delete(s.statsCache, key)
				break
			}
		}
		s.statsCache[account] = result
	}
	return result, err
}
func (s *Service) measureStatistics(ctx context.Context, account protocol.ID) (api.StorageStatistics, error) {
	result, err := s.repo.StorageStatistics(ctx, account)
	if err != nil {
		return result, err
	}
	global := result
	if account != "" {
		global, err = s.repo.StorageStatistics(ctx, "")
		if err != nil {
			return result, err
		}
	}
	available, err := s.store.Available()
	if err != nil {
		return result, err
	}
	result.AvailableBytes = max(0, available-s.policy.MinFreeBytes-global.UnwrittenBytes)
	if s.policy.GlobalBytes > 0 {
		result.AvailableBytes = min(result.AvailableBytes, max(0, s.policy.GlobalBytes-global.LogicalBytes-global.ReservedBytes))
	}
	if account != "" && s.policy.UserBytes > 0 {
		result.AvailableBytes = min(result.AvailableBytes, max(0, s.policy.UserBytes-result.LogicalBytes-result.ReservedBytes))
	}
	result.QuotaBytes = s.policy.UserBytes
	result.UpdatedAt = s.now().UTC()
	result.Transfer = s.trafficStats(account)
	scan := func(name string, dest *int64) error { n, e := s.store.Allocated(ctx, name); *dest += n; return e }
	if account == "" {
		result.QuotaBytes = s.policy.GlobalBytes
		breakdown, e := s.store.Breakdown(ctx)
		if e != nil {
			return result, e
		}
		result.CommittedBytes = breakdown["committed"]
		result.RetainedBytes = breakdown["retained"]
		result.StagedBytes = breakdown["staged"]
		result.TrashBytes = breakdown["trash"]
		result.OtherBytes = breakdown["other"]
		result.Filesystems, err = storage.Capacities(s.capacityPaths)
	} else {
		a, e := s.repo.GetAccount(ctx, account)
		if errors.Is(e, sql.ErrNoRows) {
			return result, api.Error{Code: api.NotFound, Message: "Account not found"}
		}
		if e != nil {
			return result, e
		}
		if e = scan(layout.Tree(a.Username), &result.CommittedBytes); e != nil {
			return result, e
		}
		if e = scan(".serversend/recoverable/"+string(account), &result.TrashBytes); e != nil {
			return result, e
		}
		if e = scan(layout.Versions+"/"+string(account), &result.RetainedBytes); e != nil {
			return result, e
		}
		s.stageMu.Lock()
		paths := []string{}
		for name, id := range s.stages {
			if id == account {
				paths = append(paths, name)
			}
		}
		s.stageMu.Unlock()
		for _, name := range paths {
			if e := scan(name, &result.StagedBytes); e != nil {
				return result, e
			}
		}
		err = s.repo.EachOpenUpload(ctx, account, func(id protocol.ID) error { return scan(layout.Upload(id), &result.StagedBytes) })
	}
	return result, err
}

type countedReader struct {
	io.Reader
	n int64
}

func (c *countedReader) Read(p []byte) (int, error) {
	n, e := c.Reader.Read(p)
	c.n += int64(n)
	return n, e
}
