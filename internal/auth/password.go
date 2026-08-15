// Package auth holds password hashing, session/invite token handling, CSRF
// tokens and login rate limiting (plan §4).
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Params are the argon2id parameters from plan §4. They are stored in the
// encoded hash so they can be raised later without invalidating old hashes.
type Params struct {
	Time    uint32
	Memory  uint32
	Threads uint8
	KeyLen  uint32
}

var DefaultParams = Params{Time: 1, Memory: 64 * 1024, Threads: 4, KeyLen: 32}

var ErrMismatch = errors.New("password does not match")

// HashPassword returns the standard argon2id encoded string.
func HashPassword(password string) (string, error) {
	return hashWith(password, DefaultParams)
}

func hashWith(password string, p Params) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Time, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword checks a password against an encoded hash in constant time.
func VerifyPassword(encoded, password string) error {
	p, salt, want, err := decodeHash(encoded)
	if err != nil {
		return err
	}
	got := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrMismatch
	}
	return nil
}

func decodeHash(encoded string) (p Params, salt, key []byte, err error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return p, nil, nil, errors.New("auth: unrecognized password hash format")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return p, nil, nil, err
	}
	if version != argon2.Version {
		return p, nil, nil, fmt.Errorf("auth: unsupported argon2 version %d", version)
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil {
		return p, nil, nil, err
	}
	if salt, err = base64.RawStdEncoding.Strict().DecodeString(parts[4]); err != nil {
		return p, nil, nil, err
	}
	if key, err = base64.RawStdEncoding.Strict().DecodeString(parts[5]); err != nil {
		return p, nil, nil, err
	}
	p.KeyLen = uint32(len(key))
	return p, salt, key, nil
}

// DummyVerify burns a comparable amount of CPU on a login attempt for an
// unknown username, so response timing does not enumerate accounts.
func DummyVerify(password string) {
	salt := []byte("wishd-timing-pad")
	argon2.IDKey([]byte(password), salt, DefaultParams.Time, DefaultParams.Memory,
		DefaultParams.Threads, DefaultParams.KeyLen)
}
