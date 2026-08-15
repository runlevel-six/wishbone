// Package web is the HTTP layer: router, middleware and handlers.
package web

import (
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"wishd/internal/auth"
	"wishd/internal/config"
	"wishd/internal/extract"
	"wishd/internal/imgstore"
	"wishd/internal/store"
	"wishd/internal/view"
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
		r.Get("/category-fields", s.handleCategoryFields)

		r.Post("/lists", s.handleCreateList)
		r.Get("/lists/{listID}", s.handleViewList)
		r.Post("/lists/{listID}", s.handleUpdateList)
		r.Post("/lists/{listID}/delete", s.handleDeleteList)

		r.Get("/lists/{listID}/items/new", s.handleNewItemForm)
		r.Post("/lists/{listID}/items", s.handleCreateItem)
		r.Post("/lists/{listID}/items/preview", s.handlePreviewItem)

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

func staticCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		h.ServeHTTP(w, r)
	})
}
