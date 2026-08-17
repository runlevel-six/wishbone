package web

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"wishbone/internal/auth"
	"wishbone/internal/model"
	"wishbone/internal/web/templates"
)

func routePattern(r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil {
		return rc.RoutePattern()
	}
	return r.URL.Path
}

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK,
		templates.Login(s.page(w, r, "Sign in"), "", "", safeNext(r.URL.Query().Get("next"))))
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	username := strings.TrimSpace(r.PostFormValue("username"))
	password := r.PostFormValue("password")

	const genericErr = "That username and password did not match."
	next := safeNext(r.PostFormValue("next"))

	ip := realIPFrom(ctx)
	if !s.ipLimiter.Allow(ip) || !s.userLimiter.Allow(strings.ToLower(username)) {
		s.render(w, r, http.StatusTooManyRequests,
			templates.Login(s.page(w, r, "Sign in"), username,
				"Too many attempts. Wait a few minutes and try again.", next))
		return
	}

	user, err := s.st.UserByUsername(ctx, username)
	if err != nil {
		if !errors.Is(err, model.ErrNotFound) {
			s.fail(w, r, err)
			return
		}
		// Spend comparable time so a missing account is not detectable.
		auth.DummyVerify(password)
		s.render(w, r, http.StatusUnauthorized,
			templates.Login(s.page(w, r, "Sign in"), username, genericErr, next))
		return
	}

	if err := auth.VerifyPassword(user.PasswordHash, password); err != nil {
		s.render(w, r, http.StatusUnauthorized,
			templates.Login(s.page(w, r, "Sign in"), username, genericErr, next))
		return
	}

	s.userLimiter.Reset(strings.ToLower(username))
	s.ipLimiter.Reset(ip)

	if err := s.startSession(w, r, user); err != nil {
		s.fail(w, r, err)
		return
	}
	if user.MustReset {
		s.flash(w, templates.FlashWarn, "Welcome back. Please choose a new password.")
		s.redirect(w, r, "/account")
		return
	}
	if next != "" {
		s.redirect(w, r, next)
		return
	}
	s.redirect(w, r, "/")
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, user *model.User) error {
	token := auth.NewToken()
	now := model.Now()
	expires := now.Add(s.cfg.SessionTTL)
	ua := r.UserAgent()
	if len(ua) > 200 {
		ua = ua[:200]
	}
	sess := &model.Session{
		TokenHash: auth.HashToken(token),
		UserID:    user.ID,
		CreatedAt: model.TimeString(now),
		ExpiresAt: model.TimeString(expires),
		UserAgent: &ua,
	}
	if err := s.st.CreateSession(r.Context(), sess); err != nil {
		return err
	}
	s.setSessionCookie(w, token, expires)
	return nil
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if err := s.st.DeleteSession(r.Context(), auth.HashToken(c.Value)); err != nil {
			s.log.Warn("delete session", slog.Any("err", err))
		}
	}
	s.clearCookie(w, sessionCookie)
	s.redirect(w, r, "/login")
}

func (s *Server) handleRegisterForm(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if _, err := s.st.UsableInvite(r.Context(), auth.HashToken(token), model.TimeString(model.Now())); err != nil {
		s.render(w, r, http.StatusNotFound,
			templates.BareErrorPage(s.page(w, r, "Invite"), "That invite is not usable",
				"It may have been used already, or it may have expired. Ask for a fresh link."))
		return
	}
	s.render(w, r, http.StatusOK, templates.Register(s.page(w, r, "Create account"), token, "", "", ""))
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := chi.URLParam(r, "token")
	username := strings.TrimSpace(r.PostFormValue("username"))
	displayName := strings.TrimSpace(r.PostFormValue("display_name"))
	password := r.PostFormValue("password")
	confirm := r.PostFormValue("password_confirm")

	show := func(msg string) {
		s.render(w, r, http.StatusBadRequest,
			templates.Register(s.page(w, r, "Create account"), token, username, displayName, msg))
	}

	switch {
	case len(username) < 2 || len(username) > 40:
		show("Pick a username between 2 and 40 characters.")
		return
	case strings.ContainsAny(username, " \t/?#"):
		show("Usernames cannot contain spaces or slashes.")
		return
	case displayName == "" || len(displayName) > 80:
		show("Please give a name your family will recognize.")
		return
	case len(password) < 10:
		show("Passwords must be at least 10 characters.")
		return
	case password != confirm:
		show("Those passwords do not match.")
		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	now := model.TimeString(model.Now())
	user := &model.User{
		Username:     username,
		DisplayName:  displayName,
		PasswordHash: hash,
		CreatedAt:    now,
	}
	err = s.st.RedeemInvite(ctx, auth.HashToken(token), user, now)
	switch {
	case errors.Is(err, model.ErrNotFound):
		s.render(w, r, http.StatusNotFound,
			templates.BareErrorPage(s.page(w, r, "Invite"), "That invite is not usable",
				"It may have been used already, or it may have expired."))
		return
	case errors.Is(err, model.ErrConflict):
		show("That username is taken.")
		return
	case err != nil:
		s.fail(w, r, err)
		return
	}

	if err := s.startSession(w, r, user); err != nil {
		s.fail(w, r, err)
		return
	}
	s.flash(w, templates.FlashOK, "Welcome to Wishbone.")
	s.redirect(w, r, "/")
}

func (s *Server) handleAccount(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	s.render(w, r, http.StatusOK, templates.Account(s.page(w, r, "Account"), u.MustReset, "", ""))
}

func (s *Server) handleAccountProfile(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	name := strings.TrimSpace(r.PostFormValue("display_name"))
	if name == "" || len(name) > 80 {
		s.render(w, r, http.StatusBadRequest,
			templates.Account(s.page(w, r, "Account"), u.MustReset, "That name will not work.", ""))
		return
	}
	if err := s.st.UpdateProfile(r.Context(), u.ID, name); err != nil {
		s.fail(w, r, err)
		return
	}
	s.flash(w, templates.FlashOK, "Saved.")
	s.redirect(w, r, "/account")
}

func (s *Server) handleAccountPassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	current := r.PostFormValue("current_password")
	next := r.PostFormValue("new_password")
	confirm := r.PostFormValue("new_password_confirm")

	show := func(msg string) {
		s.render(w, r, http.StatusBadRequest,
			templates.Account(s.page(w, r, "Account"), u.MustReset, msg, ""))
	}

	// An imported account has no password it knows, so the current-password
	// check is skipped exactly once (plan §9).
	if !u.MustReset {
		if err := auth.VerifyPassword(u.PasswordHash, current); err != nil {
			show("That is not your current password.")
			return
		}
	}
	if len(next) < 10 {
		show("Passwords must be at least 10 characters.")
		return
	}
	if next != confirm {
		show("Those passwords do not match.")
		return
	}

	hash, err := auth.HashPassword(next)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.st.SetPassword(ctx, u.ID, hash); err != nil {
		s.fail(w, r, err)
		return
	}
	// Every other session dies with the old password.
	if err := s.st.DeleteUserSessions(ctx, u.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.startSession(w, r, u); err != nil {
		s.fail(w, r, err)
		return
	}
	s.flash(w, templates.FlashOK, "Password changed. Other sessions were signed out.")
	s.redirect(w, r, "/")
}
