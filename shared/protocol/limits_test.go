package protocol

import (
	"math"
	"testing"
)

func testLimits() TransferLimits {
	return TransferLimits{MinChunkBytes: 4, MaxChunkBytes: 8, MaxRequestBytes: 16, MaxManifestBytes: 16, MaxChunks: 2}
}

func TestTransferLimitsValidate(t *testing.T) {
	tests := []struct {
		name   string
		change func(*TransferLimits)
		valid  bool
	}{
		{"valid", func(*TransferLimits) {}, true},
		{"zero minimum", func(l *TransferLimits) { l.MinChunkBytes = 0 }, false},
		{"negative minimum", func(l *TransferLimits) { l.MinChunkBytes = -1 }, false},
		{"reversed range", func(l *TransferLimits) { l.MaxChunkBytes = 3 }, false},
		{"equal chunk limits", func(l *TransferLimits) { l.MaxChunkBytes = 4 }, true},
		{"request below chunk", func(l *TransferLimits) { l.MaxChunkBytes = 17 }, false},
		{"request below manifest", func(l *TransferLimits) { l.MaxRequestBytes = 15 }, false},
		{"negative request", func(l *TransferLimits) { l.MaxRequestBytes = -1 }, false},
		{"zero manifest size", func(l *TransferLimits) { l.MaxManifestBytes = 0 }, false},
		{"negative manifest size", func(l *TransferLimits) { l.MaxManifestBytes = -1 }, false},
		{"zero count", func(l *TransferLimits) { l.MaxChunks = 0 }, false},
		{"negative count", func(l *TransferLimits) { l.MaxChunks = -1 }, false},
		{"largest representable bounds", func(l *TransferLimits) {
			*l = TransferLimits{1, math.MaxInt64, math.MaxInt64, math.MaxInt64, math.MaxInt64}
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limits := testLimits()
			tt.change(&limits)
			if err := limits.Validate(); (err == nil) != tt.valid {
				t.Fatalf("Validate() = %v, want valid=%v", err, tt.valid)
			}
		})
	}
}

func TestValidateChunkSize(t *testing.T) {
	for _, final := range []bool{false, true} {
		for _, size := range []int64{-1, 0, 1, 3, 4, 8, 9, math.MaxInt64} {
			valid := size > 0 && size <= 8 && (final || size >= 4)
			if err := testLimits().ValidateChunkSize(size, final); (err == nil) != valid {
				t.Errorf("size=%d final=%v: error=%v, want valid=%v", size, final, err, valid)
			}
		}
	}
	if err := (TransferLimits{}).ValidateChunkSize(1, true); err == nil {
		t.Error("invalid limits accepted")
	}
}

func TestValidateManifestBounds(t *testing.T) {
	for _, size := range []int64{-1, 0, 1, 16, 17, math.MaxInt64} {
		for _, count := range []int64{-1, 0, 1, 2, 3, math.MaxInt64} {
			valid := size > 0 && size <= 16 && count >= 0 && count <= 2
			if err := testLimits().ValidateManifestBounds(size, count); (err == nil) != valid {
				t.Errorf("bytes=%d chunks=%d: error=%v, want valid=%v", size, count, err, valid)
			}
		}
	}
	if err := (TransferLimits{}).ValidateManifestBounds(1, 0); err == nil {
		t.Error("invalid limits accepted")
	}
	// Comparing limits must not multiply counts by chunk size or overflow.
	largest := TransferLimits{1, math.MaxInt64, math.MaxInt64, math.MaxInt64, math.MaxInt64}
	if err := largest.ValidateManifestBounds(math.MaxInt64, math.MaxInt64); err != nil {
		t.Fatal(err)
	}
	if err := largest.ValidateChunkSize(math.MaxInt64, false); err != nil {
		t.Fatal(err)
	}
}
