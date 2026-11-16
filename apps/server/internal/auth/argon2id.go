package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id is the production PasswordHasher. Hashes use the PHC string format
// and carry their own parameters, so the defaults can be raised later without
// invalidating stored hashes. Each hash allocates MemoryKiB; the Service bounds
// concurrent hashing, which bounds total memory.
type Argon2id struct {
	memoryKiB   uint32
	iterations  uint32
	parallelism uint8
}

const (
	argon2SaltBytes = 16
	argon2KeyBytes  = 32
	// Verification refuses stored parameters beyond these ceilings so a
	// corrupted or hostile database row cannot exhaust memory or CPU.
	argon2MaxMemoryKiB   = 256 << 10
	argon2MaxIterations  = 10
	argon2MaxParallelism = 16
)

// NewArgon2id returns the RFC 9106 memory-constrained recommendation:
// 64 MiB, three passes, four lanes, 128-bit salt and 256-bit tag.
func NewArgon2id() Argon2id {
	return Argon2id{memoryKiB: 64 << 10, iterations: 3, parallelism: 4}
}

func (a Argon2id) Hash(password string) (string, error) {
	if a.memoryKiB < 8*uint32(a.parallelism) || a.memoryKiB > argon2MaxMemoryKiB || a.iterations < 1 || a.iterations > argon2MaxIterations || a.parallelism < 1 || a.parallelism > argon2MaxParallelism {
		return "", errors.New("invalid Argon2id parameters")
	}
	salt := make([]byte, argon2SaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", errors.New("could not generate password salt")
	}
	key := argon2.IDKey([]byte(password), salt, a.iterations, a.memoryKiB, a.parallelism, argon2KeyBytes)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, a.memoryKiB, a.iterations, a.parallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// Verify recomputes with the parameters stored in encoded. Malformed,
// unsupported or out-of-bounds hashes never match and never panic.
func (Argon2id) Verify(encoded, password string) bool {
	if len(encoded) > 256 {
		return false
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return false
	}
	var values [3]uint64
	for i, field := range strings.Split(parts[3], ",") {
		name, value, found := strings.Cut(field, "=")
		if i >= len(values) || !found || name != [...]string{"m", "t", "p"}[i] {
			return false
		}
		parsed, err := strconv.ParseUint(value, 10, 32)
		if err != nil || strconv.FormatUint(parsed, 10) != value {
			return false
		}
		values[i] = parsed
	}
	memory, iterations, parallelism := values[0], values[1], values[2]
	if parallelism < 1 || parallelism > argon2MaxParallelism || iterations < 1 || iterations > argon2MaxIterations || memory < 8*parallelism || memory > argon2MaxMemoryKiB {
		return false
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) != argon2SaltBytes {
		return false
	}
	want, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(want) != argon2KeyBytes {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, uint32(iterations), uint32(memory), uint8(parallelism), argon2KeyBytes)
	return subtle.ConstantTimeCompare(got, want) == 1
}
