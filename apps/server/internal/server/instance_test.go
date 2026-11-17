package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chironjit/serversend/apps/server/internal/instancelock"
)

func TestStartupRefusesSharedVolumesBeforeClearingIncoming(t *testing.T) {
	for _, shared := range []string{"config", "data", "staging"} {
		t.Run(shared, func(t *testing.T) {
			original := fixture(t)
			active, err := New(t.Context(), original)
			if err != nil {
				t.Fatal(err)
			}
			defer active.Close()
			marker := filepath.Join(original.DataDir, "incoming", "active-write")
			if err := os.WriteFile(marker, []byte("in progress"), 0o600); err != nil {
				t.Fatal(err)
			}
			replacement := fixture(t)
			switch shared {
			case "config":
				replacement.ConfigDir = original.ConfigDir
			case "data":
				replacement.DataDir = original.DataDir
			case "staging":
				replacement.StagingDir = original.StagingDir
			}
			if duplicate, err := New(t.Context(), replacement); err == nil {
				duplicate.Close()
				t.Fatal("accepted a second process sharing", shared)
			} else if !strings.Contains(err.Error(), "lock deployment directory") {
				t.Fatal("did not refuse duplicate by storage lock:", err)
			}
			if contents, err := os.ReadFile(marker); err != nil || string(contents) != "in progress" {
				t.Fatal("duplicate startup damaged an active write:", err)
			}
			if err := active.Close(); err != nil {
				t.Fatal(err)
			}
			// A deliberate replacement is allowed only after the original closes.
			restarted, err := New(t.Context(), replacement)
			if err != nil {
				t.Fatal("original shutdown did not permit replacement:", err)
			}
			restarted.Close()
		})
	}
}

func TestFailedStartupReleasesDeploymentLocks(t *testing.T) {
	c := fixture(t)
	if err := os.Remove(c.TLSKeyFile); err != nil {
		t.Fatal(err)
	}
	if broken, err := New(t.Context(), c); err == nil {
		broken.Close()
		t.Fatal("accepted missing TLS key")
	}
	// New must unwind acquired locks even if TLS/metadata initialization fails.
	locks, err := instancelock.Acquire(c.ConfigDir, c.DataDir, c.StagingDir)
	if err != nil {
		t.Fatal("failed startup retained deployment locks:", err)
	}
	defer locks.Close()
}
