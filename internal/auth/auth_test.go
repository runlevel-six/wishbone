package auth_test

import (
	"strings"
	"testing"
	"time"

	"wishd/internal/auth"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	hash, err := auth.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=1,p=4$") {
		t.Errorf("hash %q does not carry the parameters from plan §4", hash)
	}
	if err := auth.VerifyPassword(hash, "correct horse battery staple"); err != nil {
		t.Errorf("verify: %v", err)
	}
	if err := auth.VerifyPassword(hash, "Correct horse battery staple"); err == nil {
		t.Error("a wrong password verified")
	}
}

func TestHashesAreSalted(t *testing.T) {
	a, _ := auth.HashPassword("same password")
	b, _ := auth.HashPassword("same password")
	if a == b {
		t.Error("two hashes of the same password are identical; the salt is not random")
	}
}

func TestTokensAreHashedNotStored(t *testing.T) {
	token := auth.NewToken()
	if len(token) < 40 {
		t.Errorf("token %q is shorter than 32 random bytes base64url", token)
	}
	h := auth.HashToken(token)
	if strings.Contains(h, token) || len(h) != 64 {
		t.Errorf("HashToken returned %q, want a hex sha256", h)
	}
	if auth.HashToken(token) != h {
		t.Error("HashToken is not deterministic")
	}
}

func TestCSRFTokenBoundToSession(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	a := auth.CSRFToken(secret, "session-a")
	b := auth.CSRFToken(secret, "session-b")

	if a == b {
		t.Error("two sessions share a CSRF token")
	}
	if !auth.CheckCSRF(secret, "session-a", a) {
		t.Error("a session's own token was rejected")
	}
	if auth.CheckCSRF(secret, "session-a", b) {
		t.Error("another session's token was accepted")
	}
	if auth.CheckCSRF(secret, "session-a", "") {
		t.Error("an empty token was accepted")
	}
	if auth.CheckCSRF([]byte("a different secret key 0123456"), "session-a", a) {
		t.Error("a token minted under another secret was accepted")
	}
}

func TestLimiterBurstThenRefusal(t *testing.T) {
	l := auth.NewLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		if !l.Allow("alice") {
			t.Fatalf("attempt %d should be allowed within the burst", i+1)
		}
	}
	if l.Allow("alice") {
		t.Error("the fourth attempt should be refused")
	}
	if !l.Allow("bob") {
		t.Error("one user's attempts should not throttle another")
	}
	l.Reset("alice")
	if !l.Allow("alice") {
		t.Error("a successful sign-in should clear the bucket")
	}
}
