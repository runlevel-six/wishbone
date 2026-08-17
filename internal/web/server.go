// Package web is the HTTP layer: router, middleware and handlers.
package web

import (
	"embed"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"wishbone/internal/auth"
	"wishbone/internal/config"
	"wishbone/internal/extract"
	"wishbone/internal/imgstore"
	"wishbone/internal/store"
	"wishbone/internal/view"
)

//go:embed static
var staticFS embed.FS

type Server struct {
	cfg *config.Config
	st  *store.Store
	vb  *view.Builder
	ex  *extract.Service
	img *imgstore.Store
	log *slog.Logger

	userLimiter *auth.Limiter
	ipLimiter   *auth.Limiter

	router chi.Router
}

func init() {
	// Not in Go's built-in table, and a manifest served as text/plain is
	// ignored by some browsers.
	if err := mime.AddExtensionType(".webmanifest", "application/manifest+json"); err != nil {
		panic("web: register manifest mime type: " + err.Error())
	}
}

func NewServer(cfg *config.Config, st *store.Store, ex *extract.Service, img *imgstore.Store, log *slog.Logger) *Server {
	s := &Server{
		cfg: cfg,
		st:  st,
		vb:  view.New(st),
		ex:  ex,
		img: img,
		log: log,
		// Generous enough for a family fumbling a password, tight enough that
		// online guessing is hopeless.
		userLimiter: auth.NewLimiter(8, 15*time.Minute),
		ipLimiter:   auth.NewLimiter(30, 15*time.Minute),
	}
	s.routes()
	return s
}

func (s *Server) Router() chi.Router { return s.router }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

// SweepLimiters is called periodically by the background janitor.
func (s *Server) SweepLimiters() {
	s.userLimiter.Sweep()
	s.ipLimiter.Sweep()
}

func (s *Server) routes() {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(s.realIP)
	r.Use(s.recoverer)
	r.Use(s.requestLogger)
	r.Use(s.securityHeaders)
	r.Use(middleware.Timeout(30 * time.Second))
	r.Use(s.loadSession)
	r.Use(s.csrf)

	// Health probes are unauthenticated by necessity (plan §10).
	r.Get("/healthz", s.handleHealthz)
	r.Get("/readyz", s.handleReadyz)

	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic("web: static assets: " + err.Error())
	}
	r.Handle("/static/*", http.StripPrefix("/static/", staticCache(http.FileServer(http.FS(sub)))))

	// The service worker must be served from the root: its scope is the
	// directory it is served from, and one scoped to /static/ could not
	// control the app.
	r.Get("/sw.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, sub, "sw.js")
	})

	r.Group(func(r chi.Router) {
		r.Use(s.requireAnonymous)
		r.Get("/login", s.handleLoginForm)
		r.Post("/login", s.handleLogin)
		r.Get("/register/{token}", s.handleRegisterForm)
		r.Post("/register/{token}", s.handleRegister)
	})

	r.Group(func(r chi.Router) {
		r.Use(s.requireUser)

		r.Get("/", s.handleDashboard)
		r.Post("/logout", s.handleLogout)

		r.Get("/account", s.handleAccount)
		r.Post("/account/profile", s.handleAccountProfile)
		r.Post("/account/password", s.handleAccountPassword)

		r.Get("/claims", s.handleMyClaims)
		r.Get("/share-target", s.handleShareTarget)
		r.Get("/category-fields", s.handleCategoryFields)

		r.Post("/lists", s.handleCreateList)
		r.Get("/lists/{listID}", s.handleViewList)
		r.Post("/lists/{listID}", s.handleUpdateList)
		r.Post("/lists/{listID}/delete", s.handleDeleteList)

		r.Get("/lists/{listID}/items/new", s.handleNewItemForm)
		r.Post("/lists/{listID}/items", s.handleCreateItem)
		r.Post("/lists/{listID}/items/preview", s.handlePreviewItem)
		r.Post("/lists/{listID}/items/preview/accept", s.handleAcceptSuspectPreview)

		r.Get("/items/{itemID}/edit", s.handleEditItemForm)
		r.Post("/items/{itemID}", s.handleUpdateItem)
		r.Post("/items/{itemID}/delete", s.handleDeleteItem)
		r.Post("/items/{itemID}/move", s.handleMoveItem)

		r.Post("/items/{itemID}/claims", s.handleCreateClaim)
		r.Post("/claims/{claimID}/release", s.handleReleaseClaim)
		r.Post("/claims/{claimID}/state", s.handleClaimState)
		r.Post("/claims/{claimID}/note", s.handleClaimNote)

		r.Get("/images/{sha}", s.handleImage)

		r.Group(func(r chi.Router) {
			r.Use(s.requireAdmin)
			r.Get("/admin", s.handleAdmin)
			r.Get("/admin/health", s.handleAdminHealth)
			r.Post("/admin/invites", s.handleCreateInvite)
			r.Post("/admin/invites/{tokenHash}/delete", s.handleDeleteInvite)
			r.Post("/admin/users/{userID}/admin", s.handleSetAdmin)
		})
	})

	r.NotFound(s.handleNotFound)
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	})

	s.router = r
}

// staticCache sets how long /static/ may be held.
//
// A request carrying a version is safe to keep forever: the URL changes with
// every build, so a cached copy can never be the wrong copy. One without a
// version might be anything — a hand-typed URL, an older page still in a tab —
// and gets an hour, which is short enough that a release reaches it soon and
// long enough to be worth having.
func staticCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("v") != "" {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		h.ServeHTTP(w, r)
	})
}
