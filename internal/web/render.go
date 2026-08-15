package web

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/a-h/templ"

	"wishd/internal/model"
	"wishd/internal/web/templates"
)

// page assembles the per-request chrome, consuming any pending flash.
func (s *Server) page(w http.ResponseWriter, r *http.Request, title string) templates.Page {
	return templates.Page{
		Title:   title,
		User:    userFrom(r.Context()),
		CSRF:    csrfFrom(r.Context()),
		Flashes: s.takeFlashes(w, r),
		Path:    r.URL.Path,
	}
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Wishlists are personal; never let a shared cache hold one.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := c.Render(r.Context(), w); err != nil {
		s.log.Error("render", slog.String("path", r.URL.Path), slog.Any("err", err))
	}
}

// flash queues a one-shot message for the next rendered page.
func (s *Server) flash(w http.ResponseWriter, kind, text string) {
	v := url.QueryEscape(kind + "|" + text)
	http.SetCookie(w, &http.Cookie{
		Name:     flashCookie,
		Value:    v,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.SecureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   120,
	})
}

func (s *Server) takeFlashes(w http.ResponseWriter, r *http.Request) []templates.Flash {
	c, err := r.Cookie(flashCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	s.clearCookie(w, flashCookie)
	raw, err := url.QueryUnescape(c.Value)
	if err != nil {
		return nil
	}
	kind, text, ok := strings.Cut(raw, "|")
	if !ok || text == "" {
		return nil
	}
	return []templates.Flash{{Kind: kind, Text: text}}
}

// redirect sends the browser onward, honoring htmx.
func (s *Server) redirect(w http.ResponseWriter, r *http.Request, to string) {
	if isHTMX(r) {
		w.Header().Set("HX-Redirect", to)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// back returns to the referring page, defaulting to the dashboard.
func (s *Server) back(w http.ResponseWriter, r *http.Request, fallback string) {
	to := fallback
	if ref := r.Referer(); ref != "" {
		if u, err := url.Parse(ref); err == nil && u.Host == r.Host {
			to = u.RequestURI()
		}
	}
	s.redirect(w, r, to)
}

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	s.renderNotFound(w, r)
}

// renderNotFound is used for both "no such thing" and "you may not see this".
// The two are deliberately indistinguishable (plan §3.1).
func (s *Server) renderNotFound(w http.ResponseWriter, r *http.Request) {
	if userFrom(r.Context()) == nil {
		s.render(w, r, http.StatusNotFound,
			templates.BareErrorPage(s.page(w, r, "Not found"), "Not found",
				"That page does not exist, or you need to sign in to see it."))
		return
	}
	s.render(w, r, http.StatusNotFound,
		templates.ErrorPage(s.page(w, r, "Not found"), http.StatusNotFound, "Not found",
			"That page does not exist, or it is not shared with you."))
}

// fail maps a store error onto a response. ErrOwnerBlind is treated as a bug
// caught at the last line of defense: it means a handler asked for claim data
// on behalf of the list owner, which should be impossible by construction.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, model.ErrNotFound):
		s.renderNotFound(w, r)
	case errors.Is(err, model.ErrForbidden):
		s.renderNotFound(w, r)
	case errors.Is(err, model.ErrOwnerBlind):
		s.log.Error("owner-blindness guard tripped",
			slog.String("path", r.URL.Path),
			slog.String("route", routePattern(r)))
		s.render(w, r, http.StatusInternalServerError,
			templates.ErrorPage(s.page(w, r, "Error"), 500, "Something went wrong",
				"Nothing was shown, which is the safe outcome. This has been logged."))
	case errors.Is(err, context.Canceled):
		// Client went away; nothing to render.
	default:
		s.log.Error("request failed", slog.String("path", r.URL.Path), slog.Any("err", err))
		s.render(w, r, http.StatusInternalServerError,
			templates.ErrorPage(s.page(w, r, "Error"), 500, "Something went wrong",
				"Try again in a moment. If it keeps happening, tell whoever runs this."))
	}
}
