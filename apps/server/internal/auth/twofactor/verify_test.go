package twofactor

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// RFC 6238 Appendix B SHA1 test secret and the last six digits of its
// eight-digit vectors (decimal truncation to six digits).
const vectorSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

func TestRFC6238Vectors(t *testing.T) {
	for _, tt := range []struct {
		seconds int64
		code    string
	}{
		{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"},
		{1234567890, "005924"}, {2000000000, "279037"}, {20000000000, "353130"},
	} {
		step, err := Verify(vectorSecret, tt.code, time.Unix(tt.seconds, 0), NoAcceptedStep)
		if err != nil || step != tt.seconds/30 {
			t.Fatalf("timestamp %d: got step=%d err=%v", tt.seconds, step, err)
		}
	}
}

func TestSkewAndPeriodBoundaries(t *testing.T) {
	// RFC 4226 counter 1 is 287082. It is accepted during counter 0, 1, or 2.
	for _, seconds := range []int64{0, 29, 30, 59, 60, 89} {
		step, err := Verify(vectorSecret, "287082", time.Unix(seconds, 0), NoAcceptedStep)
		if err != nil || step != 1 {
			t.Fatalf("timestamp %d: step=%d err=%v", seconds, step, err)
		}
	}
	for _, seconds := range []int64{90, 120} {
		_, err := Verify(vectorSecret, "287082", time.Unix(seconds, 0), NoAcceptedStep)
		if !errors.Is(err, ErrInvalidCode) {
			t.Fatalf("timestamp %d: got %v", seconds, err)
		}
	}
	// Counter 2 (359152) is outside the future window at counter 0.
	if _, err := Verify(vectorSecret, "359152", time.Unix(29, 999999999), NoAcceptedStep); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("future code: %v", err)
	}
	// Counter 0 is valid at the epoch; the previous step must not underflow.
	if step, err := Verify(vectorSecret, "755224", time.Unix(0, 0), NoAcceptedStep); err != nil || step != 0 {
		t.Fatalf("epoch: step=%d err=%v", step, err)
	}
}

func TestReplayWatermark(t *testing.T) {
	for _, tt := range []struct {
		now, last int64
		code      string
		wantStep  int64
		wantErr   error
	}{
		{30, -1, "287082", 1, nil},
		{30, 1, "287082", -1, ErrReplay},
		{60, 1, "287082", -1, ErrReplay},
		{30, 1, "755224", -1, ErrReplay},
		{30, 1, "359152", 2, nil},
		{30, 2, "359152", -1, ErrReplay},
		{60, 2, "359152", -1, ErrReplay},
		{30, 2, "287082", -1, ErrReplay},
	} {
		step, err := Verify(vectorSecret, tt.code, time.Unix(tt.now, 0), tt.last)
		if step != tt.wantStep || !errors.Is(err, tt.wantErr) {
			t.Fatalf("now=%d last=%d: step=%d err=%v", tt.now, tt.last, step, err)
		}
	}
}

func TestInvalidVerificationInput(t *testing.T) {
	for _, code := range []string{"", "28708", "2870820", "28708a", "+87082", " 87082", "287082 ", "２８７０８２", "28\x00082", "000000"} {
		step, err := Verify(vectorSecret, code, time.Unix(59, 0), NoAcceptedStep)
		if step != NoAcceptedStep || !errors.Is(err, ErrInvalidCode) {
			t.Fatalf("invalid code returned step=%d err=%v", step, err)
		}
	}
	for _, secret := range []string{"", "A", vectorSecret + "=", strings.ToLower(vectorSecret), strings.Repeat("!", 32), strings.Repeat("0", 32), vectorSecret + " "} {
		step, err := Verify(secret, "287082", time.Unix(59, 0), NoAcceptedStep)
		if step != NoAcceptedStep || !errors.Is(err, ErrInvalidSecret) {
			t.Fatalf("invalid secret returned step=%d err=%v", step, err)
		}
	}
	if _, err := Verify(vectorSecret, "287082", time.Unix(-1, 999999999), NoAcceptedStep); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("pre-epoch: %v", err)
	}
	if _, err := Verify(vectorSecret, "287082", time.Unix(59, 0), -2); !errors.Is(err, ErrInvalidStep) {
		t.Fatalf("invalid watermark: %v", err)
	}
}
