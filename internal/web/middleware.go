package web

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"

	"wishbone/internal/auth"
	"wishbone/internal/model"
)

type ctxKey int

const (
	ctxUser ctxKey = iota
	ctxSessionHash
	ctxCSRF
	ctxRealIP
)

const (
	sessionCookie = "wishbone_session"
	csrfCookie    = "wishbone_csrf"
	flashCookie   = "wishbone_flash"
	csrfHeader    = "X-CSRF-Token"
	csrfField     = "csrf_token"
)

func userFrom(ctx context.Context) *model.User {
	u, _ := ctx.Value(ctxUser).(*model.User)
	return u
}

func csrfFrom(ctx context.Context) string {
	t, _ := ctx.Value(ctxCSRF).(string)
	return t
}

func realIPFrom(ctx context.Context) string {
	ip, _ := ctx.Value(ctxRealIP).(string)
	return ip
}

// realIP trusts X-Forwarded-For only from configured proxy CIDRs (plan §4).
// Anything else uses the socket address, so a client cannot spoof its way past
// the login rate limiter.
func (s *Server) realIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remote := r.RemoteAddr
		if host, _, err := net.SplitHostPort(remote); err == nil {
			remote = host
		}
		ip := remote
		if s.trustedProxy(remote) {
			if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				// Right-most untrusted entry is the real client.
				parts := strings.Split(xff, ",")
				for i := len(parts) - 1; i >= 0; i-- {
					candidate := strings.TrimSpace(parts[i])
					if candidate == "" {
						continue
					}
					if !s.trustedProxy(candidate) {
						ip = candidate
						break
					}
				}
			} else if xrip := strings.TrimSpace(r.Header.Get("X-Real-IP")); xrip != "" {
				ip = xrip
			}
		}
		ctx := context.WithValue(r.Context(), ctxRealIP, ip)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) trustedProxy(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	for _, n := range s.cfg.TrustedProxies {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		// Everything is same-origin: no CDN, no external fonts, no analytics.
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; "+
				"form-action 'self'; frame-ancestors 'none'; base-uri 'none'; object-src 'none'")
		if s.cfg.SecureCookies {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("panic serving request",
					slog.Any("panic", rec),
					slog.String("path", r.URL.Path),
					slog.String("stack", string(debug.Stack())))
				http.Error(w, "something went wrong", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" || strings.HasPrefix(r.URL.Path, "/static/") {
			return
		}
		s.log.Info("request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", sw.status),
			slog.Duration("took", time.Since(start)))
	})
}

// loadSession resolves the session cookie and slides its expiry.
func (s *Server) loadSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		c, err := r.Cookie(sessionCookie)
		if err == nil && c.Value != "" {
			hash := auth.HashToken(c.Value)
			now := model.Now()
			user, sess, err := s.st.SessionUser(ctx, hash, model.TimeString(now))
			switch {
			case err == nil:
				ctx = context.WithValue(ctx, ctxUser, user)
				ctx = context.WithValue(ctx, ctxSessionHash, hash)
				// Sliding renewal: only write when it actually moves, so a
				// browsing session is not one UPDATE per request.
				if exp, perr := model.ParseTime(sess.ExpiresAt); perr == nil {
					if time.Until(exp) < s.cfg.SessionTTL-time.Hour {
						newExp := model.TimeString(now.Add(s.cfg.SessionTTL))
						if err := s.st.TouchSession(ctx, hash, newExp); err != nil {
							s.log.Warn("touch session", slog.Any("err", err))
						}
						s.setSessionCookie(w, c.Value, now.Add(s.cfg.SessionTTL))
					}
				}
			case errors.Is(err, model.ErrNotFound):
				s.clearCookie(w, sessionCookie)
			default:
				s.log.Error("session lookup", slog.Any("err", err))
			}
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// csrf derives a per-session token and enforces it on every mutating request
// (plan §4). Anonymous forms (sign in, register) are covered by a separate
// cookie so the login POST is protected too.
func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		key, _ := ctx.Value(ctxSessionHash).(string)
		if key == "" {
			c, err := r.Cookie(csrfCookie)
			if err != nil || c.Value == "" {
				key = auth.NewToken()
				http.SetCookie(w, &http.Cookie{
					Name:     csrfCookie,
					Value:    key,
					Path:     "/",
					HttpOnly: true,
					Secure:   s.cfg.SecureCookies,
					SameSite: http.SameSiteLaxMode,
					MaxAge:   int((2 * time.Hour).Seconds()),
				})
			} else {
				key = c.Value
			}
		}
		token := auth.CSRFToken(s.cfg.SecretKey, key)
		ctx = context.WithValue(ctx, ctxCSRF, token)
		r = r.WithContext(ctx)

		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}

		submitted := r.Header.Get(csrfHeader)
		if submitted == "" {
			// ParseForm here is safe: handlers call it again and it is
			// idempotent. Multipart bodies carry the token in the header
			// instead, via hx-headers, or in the parsed form below.
			if err := r.ParseMultipartForm(8 << 20); err != nil {
				_ = r.ParseForm()
			}
			submitted = r.PostFormValue(csrfField)
		}
		if !auth.CheckCSRF(s.cfg.SecretKey, key, submitted) {
			s.log.Warn("csrf rejected", slog.String("path", r.URL.Path))
			http.Error(w, "Your session expired. Go back, reload the page and try again.",
				http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := userFrom(r.Context())
		if u == nil {
			// Carry the destination through sign-in, so a link shared from a
			// phone survives an expired session instead of dumping the person
			// on the dashboard with nothing.
			to := "/login"
			if r.Method == http.MethodGet {
				if next := safeNext(r.URL.RequestURI()); next != "" && next != "/" {
					to += "?next=" + url.QueryEscape(next)
				}
			}
			if isHTMX(r) {
				w.Header().Set("HX-Redirect", to)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, to, http.StatusSeeOther)
			return
		}
		// An imported account must set a password before doing anything else
		// (plan §9).
		if u.MustReset && !strings.HasPrefix(r.URL.Path, "/account") && r.URL.Path != "/logout" {
			http.Redirect(w, r, "/account", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireAnonymous(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if userFrom(r.Context()) != nil {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := userFrom(r.Context())
		if u == nil || !u.IsAdmin {
			s.renderNotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isHTMX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

func (s *Server) setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.SecureCookies,
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
	})
}

func (s *Server) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.SecureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}
