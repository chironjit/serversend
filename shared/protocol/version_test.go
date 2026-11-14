package protocol

import (
	"errors"
	"testing"
)

func TestNegotiateVersion(t *testing.T) {
	tests := []struct {
		name          string
		local, remote []Version
		want          Version
		wantErr       bool
	}{
		{"current", []Version{Version1}, []Version{Version1}, Version1, false},
		{"highest common unsorted", []Version{3, 1, 2}, []Version{2, 4, 3}, 3, false},
		{"future peer retains compatibility", []Version{1}, []Version{9, 1}, 1, false},
		{"no implicit lower support", []Version{1}, []Version{2}, 0, true},
		{"empty local", nil, []Version{1}, 0, true},
		{"empty remote", []Version{1}, nil, 0, true},
		{"zero local", []Version{0, 1}, []Version{1}, 0, true},
		{"zero remote", []Version{1}, []Version{1, 0}, 0, true},
		{"duplicate local", []Version{1, 1}, []Version{1}, 0, true},
		{"duplicate remote", []Version{1}, []Version{1, 1}, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NegotiateVersion(tt.local, tt.remote)
			if got != tt.want || (err != nil) != tt.wantErr {
				t.Fatalf("got (%d, %v), want (%d, error=%v)", got, err, tt.want, tt.wantErr)
			}
		})
	}
	if _, err := NegotiateVersion([]Version{1}, []Version{2}); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("got %v, want ErrUnsupportedVersion", err)
	}
}
