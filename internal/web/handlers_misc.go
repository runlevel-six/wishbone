package web

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"wishbone/internal/auth"
	"wishbone/internal/model"
	"wishbone/internal/web/templates"
)

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	io.WriteString(w, "ok\n")
}

// handleReadyz checks the database is writable, which is what "ready" means
// for a single-writer SQLite app (plan §10).
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if err := s.st.Writable(r.Context()); err != nil {
		http.Error(w, "not ready: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	io.WriteString(w, "ready\n")
}

// handleImage serves a stored blob to an authenticated viewer who can see at
// least one item referencing it (plan §6). There is no unauthenticated image
// route: a guessable hash would otherwise leak wishlist contents.
func (s *Server) handleImage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	sha := strings.ToLower(chi.URLParam(r, "sha"))

	if _, err := s.st.ImageAccessible(ctx, sha, u.ID); err != nil {
		if errors.Is(err, model.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		s.fail(w, r, err)
		return
	}

	f, mime, err := s.img.Open(sha, r.URL.Query().Get("full") == "")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		s.fail(w, r, err)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", mime)
	// Content-addressed, so the bytes for a URL never change; private because
	// authorization is per viewer.
	w.Header().Set("Cache-Control", "private, max-age=86400, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if rs, ok := f.(io.ReadSeeker); ok {
		// A zero modtime tells ServeContent to skip Last-Modified: the bytes
		// behind a content-addressed URL never change, so a timestamp that
		// moves every request would only defeat the cache.
		http.ServeContent(w, r, sha, time.Time{}, rs)
		return
	}
	io.Copy(w, f)
}

// handleHelp renders the in-app manual (templates.Help).
//
// It is the only page besides sign-in that an anonymous reader may see, and that
// is the point of it: two of the things people most need help with — a forgotten
// password, and how to get an invite at all — are things you cannot get past in
// order to reach a page that is behind the session check. Serving it costs
// nothing, because the page reads no data: it is prose plus this instance's own
// address and whether link lookup is on.
func (s *Server) handleHelp(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, templates.Help(s.page(w, r, "Help"), templates.HelpData{
		ShareTargetURL: s.baseURL(r) + "/share-target",
		FetchEnabled:   s.ex.Enabled(),
	}))
}

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	users, err := s.st.ListUsers(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	invites, err := s.st.ListInvites(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	stats, err := s.st.Stats(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	drift, err := s.st.CheckClaimInvariant(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	nameOf := map[string]string{}
	for _, u := range users {
		nameOf[u.ID] = u.DisplayName
	}
	var rows []templates.InviteRow
	for _, inv := range invites {
		row := templates.InviteRow{
			TokenHash: inv.TokenHash,
			CreatedBy: nameOf[inv.CreatedBy],
			CreatedAt: inv.CreatedAt,
			ExpiresAt: inv.ExpiresAt,
			Used:      inv.UsedAt != nil,
		}
		if inv.UsedBy != nil {
			row.UsedBy = nameOf[*inv.UsedBy]
		}
		rows = append(rows, row)
	}

	data := templates.AdminData{
		Users:   users,
		Invites: rows,
		Stats: templates.AdminStats{
			Users: stats.Users, Lists: stats.Lists, Items: stats.Items, Images: stats.Images,
		},
		InviteLink:   s.inviteLink(r),
		ClaimDrift:   len(drift),
		SecretWarn:   s.cfg.SecretIsEphemeral,
		SidecarOn:    s.cfg.SidecarURL != "",
		FetchEnabled: s.ex.Enabled(),
	}
	s.render(w, r, http.StatusOK, templates.Admin(s.page(w, r, "Admin"), data))
}

// handleAdminHealth reports the claimed_qty invariant (plan §2.1). It reports
// counts of drifting items, never who claimed what.
func (s *Server) handleAdminHealth(w http.ResponseWriter, r *http.Request) {
	drift, err := s.st.CheckClaimInvariant(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if len(drift) == 0 {
		io.WriteString(w, "claim invariant: ok\n")
		return
	}
	w.WriteHeader(http.StatusInternalServerError)
	io.WriteString(w, "claim invariant: DRIFT\n")
	for _, d := range drift {
		fmt.Fprintf(w, "%s claimed_qty=%d sum(claims)=%d\n", d.ItemID, d.ClaimedQty, d.SumClaims)
	}
}

func (s *Server) handleCreateInvite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)

	token := auth.NewToken()
	now := model.Now()
	inv := &model.Invite{
		TokenHash: auth.HashToken(token),
		CreatedBy: u.ID,
		CreatedAt: model.TimeString(now),
		ExpiresAt: model.TimeString(now.Add(s.cfg.InviteTTL)),
	}
	if err := s.st.CreateInvite(ctx, inv); err != nil {
		s.fail(w, r, err)
		return
	}
	// The link is shown once, on the redirect target. Only the hash is stored.
	s.redirect(w, r, "/admin?invite="+token)
}

func (s *Server) handleDeleteInvite(w http.ResponseWriter, r *http.Request) {
	if err := s.st.DeleteInvite(r.Context(), chi.URLParam(r, "tokenHash")); err != nil {
		s.fail(w, r, err)
		return
	}
	s.flash(w, templates.FlashOK, "Invite revoked.")
	s.redirect(w, r, "/admin")
}

func (s *Server) handleSetAdmin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me := userFrom(ctx)
	target := chi.URLParam(r, "userID")
	makeAdmin := r.PostFormValue("admin") == "1"

	if target == me.ID && !makeAdmin {
		s.flash(w, templates.FlashWarn, "Removing your own admin rights would lock you out; do it from another admin account.")
		s.redirect(w, r, "/admin")
		return
	}
	if err := s.st.SetAdmin(ctx, target, makeAdmin); err != nil {
		s.fail(w, r, err)
		return
	}
	s.flash(w, templates.FlashOK, "Updated.")
	s.redirect(w, r, "/admin")
}

// inviteLink turns the one-time token from the redirect into a full URL for
// copy-paste. Wishbone sends no email (plan §0), so the admin passes it on
// however they like.
func (s *Server) inviteLink(r *http.Request) string {
	token := r.URL.Query().Get("invite")
	if token == "" {
		return ""
	}
	return s.baseURL(r) + "/register/" + token
}

// baseURL is this instance's own address, for the two places that have to hand
// somebody a whole URL to copy: an invite link, and the share-target address the
// iPhone shortcut is built around. Configured if it is configured, and otherwise
// the host this request arrived on, which is right in every deployment that is
// not behind a rewriting proxy.
func (s *Server) baseURL(r *http.Request) string {
	if s.cfg.BaseURL != "" {
		return s.cfg.BaseURL
	}
	scheme := "https"
	if !s.cfg.SecureCookies {
		scheme = "http"
	}
	return scheme + "://" + r.Host
}
