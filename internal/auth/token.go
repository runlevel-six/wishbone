package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

// NewToken returns 32 random bytes, base64url-encoded (plan §4). Used for both
// session cookies and invite links.
func NewToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("wishd: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// HashToken returns the hex sha256 of a token. Only hashes are stored, so a
// database copy cannot be replayed as a live session or an unused invite.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CSRFToken derives a per-session CSRF token from the session token hash and
// the server secret. Deriving rather than storing keeps the sessions table
// unchanged and makes the token stable for the life of the session.
func CSRFToken(secret []byte, sessionTokenHash string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("csrf:"))
	mac.Write([]byte(sessionTokenHash))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// CheckCSRF compares a submitted token against the expected one in constant
// time.
func CheckCSRF(secret []byte, sessionTokenHash, submitted string) bool {
	want := CSRFToken(secret, sessionTokenHash)
	return hmac.Equal([]byte(want), []byte(submitted))
}
