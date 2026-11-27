// Package web serves Eimsky's web app: the factory portal, Eimsky ops and
// admin, and the association dashboard.
package web

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/itsmepava/container/internal/app"
	"github.com/itsmepava/container/internal/domain"
)

//go:embed templates static
var assets embed.FS

const sessionCookie = "eimsky_session"
const flashCookie = "eimsky_flash"

type Server struct {
	app     *app.App
	pages   map[string]*template.Template
	limiter *limiter
	static  http.Handler
	API     http.Handler // mobile API, mounted at /api
}

func New(a *app.App) (*Server, error) {
	s := &Server{app: a, pages: map[string]*template.Template{}, limiter: newLimiter()}
	if err := s.loadTemplates(); err != nil {
		return nil, err
	}
	sub, _ := fs.Sub(assets, "static")
	s.static = http.StripPrefix("/static/", cacheStatic(http.FileServer(http.FS(sub))))
	return s, nil
}

func cacheStatic(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "fonts/") || strings.HasPrefix(r.URL.Path, "vendor/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=300")
		}
		h.ServeHTTP(w, r)
	})
}

func (s *Server) loadTemplates() error {
	base := template.New("").Funcs(funcs)
	base, err := base.ParseFS(assets, "templates/layout/*.html", "templates/partials/*.html")
	if err != nil {
		return err
	}
	pages, err := fs.Glob(assets, "templates/pages/*/*.html")
	if err != nil {
		return err
	}
	for _, p := range pages {
		t, err := base.Clone()
		if err != nil {
			return err
		}
		if _, err := t.ParseFS(assets, p); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		name := strings.TrimSuffix(strings.TrimPrefix(p, "templates/pages/"), ".html")
		s.pages[name] = t
	}
	return nil
}

// Handler builds the router.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RealIP, middleware.RequestID, s.logRequests, s.recoverer, securityHeaders)
	r.Handle("/static/*", s.static)
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.app.DB.Ping(r.Context()); err != nil {
			http.Error(w, "db down", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok"))
	})
	r.Get("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Write([]byte(faviconSVG))
	})
	if s.API != nil {
		r.Mount("/api", s.API)
	}

	r.Group(func(r chi.Router) {
		r.Use(s.loadSession, s.csrf)
		s.authRoutes(r)

		r.Group(func(r chi.Router) {
			r.Use(s.requireUser)
			r.Get("/", s.home)
			s.commonRoutes(r)
			r.Route("/f", func(r chi.Router) {
				r.Use(s.requireRole(func(u *app.User) bool { return u.IsFactory() }))
				s.factoryRoutes(r)
			})
			r.Route("/ops", func(r chi.Router) {
				r.Use(s.requireRole(func(u *app.User) bool { return u.IsStaff() }))
				s.opsRoutes(r)
			})
			r.Route("/admin", func(r chi.Router) {
				r.Use(s.requireRole(func(u *app.User) bool { return u.IsStaff() }))
				s.adminRoutes(r)
			})
			r.With(s.requireRole(func(u *app.User) bool { return u.IsViewer() || u.IsStaff() })).Get("/insights", s.insights)
			if s.app.Cfg.DevMode {
				r.With(s.requireRole(func(u *app.User) bool { return u.IsStaff() })).Get("/dev/mail", s.devMail)
			}
		})
	})
	r.NotFound(func(w http.ResponseWriter, r *http.Request) { s.notFound(w, r) })
	return r
}

const faviconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect width="32" height="32" rx="7" fill="#0b4ea2"/><rect x="6" y="11" width="6" height="10" fill="#fff"/><rect x="13" y="11" width="5" height="10" fill="#b9cff0"/><rect x="19" y="11" width="7" height="10" fill="#fff" opacity=".7"/></svg>`

// ---------- Context ----------

type ctxKey int

const (
	keySession ctxKey = iota
)

func sessionOf(r *http.Request) *app.Session {
	s, _ := r.Context().Value(keySession).(*app.Session)
	return s
}

func userOf(r *http.Request) *app.User {
	if s := sessionOf(r); s != nil {
		return s.User
	}
	return nil
}

// ---------- Middleware ----------

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		if strings.HasPrefix(r.URL.Path, "/static/") {
			return
		}
		s.app.Log.Info("http", "method", r.Method, "path", r.URL.Path, "status", ww.Status(), "ms", time.Since(start).Milliseconds())
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				s.app.Log.Error("panic", "err", rec, "path", r.URL.Path)
				http.Error(w, "Something went wrong on our side. Please try again.", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) loadSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
			if sess, err := s.app.SessionByToken(r.Context(), c.Value, "web"); err == nil {
				r = r.WithContext(context.WithValue(r.Context(), keySession, sess))
			} else {
				s.clearCookie(w, sessionCookie)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// csrf checks the session token on state-changing requests, and the Origin
// for anonymous forms (sign-in, sign-up).
func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		if o := r.Header.Get("Origin"); o != "" {
			if u, err := url.Parse(o); err != nil || u.Host != r.Host {
				http.Error(w, "Cross-site request blocked.", http.StatusForbidden)
				return
			}
		}
		if sess := sessionOf(r); sess != nil {
			tok := r.Header.Get("X-CSRF-Token")
			if tok == "" {
				if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
					_ = r.ParseMultipartForm(12 << 20)
				}
				tok = r.FormValue("csrf")
			}
			if !app.CheckCSRF(sess.CSRF, tok) {
				s.flash(w, "bad", "Your session expired. Please try again.")
				http.Redirect(w, r, backURL(r, "/"), http.StatusSeeOther)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if userOf(r) == nil {
			if r.Header.Get("HX-Request") != "" {
				w.Header().Set("HX-Redirect", "/login")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			next := r.URL.RequestURI()
			http.Redirect(w, r, "/login?next="+url.QueryEscape(next), http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireRole(ok func(*app.User) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !ok(userOf(r)) {
				s.notFound(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ---------- Rate limiting ----------

type limiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func newLimiter() *limiter { return &limiter{hits: map[string][]time.Time{}} }

// Allow reports whether key may make another request: at most n per window.
func (l *limiter) Allow(key string, n int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	var keep []time.Time
	for _, t := range l.hits[key] {
		if now.Sub(t) < window {
			keep = append(keep, t)
		}
	}
	if len(keep) >= n {
		l.hits[key] = keep
		return false
	}
	l.hits[key] = append(keep, now)
	if len(l.hits) > 10000 {
		l.hits = map[string][]time.Time{}
	}
	return true
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ---------- Rendering ----------

type Flash struct {
	Kind string // good, bad, info, warn
	Msg  string
}

type Page struct {
	Title   string
	Layout  string // app, auth
	Section string
	Area    string // factory, ops, viewer
	Heading string
	Eyebrow string
	User    *app.User
	CSRF    string
	Flash   *Flash
	Unread  int
	Pending int
	Dev     bool
	Now     time.Time
	Path    string
	Query   url.Values
	D       any
}

func (s *Server) page(r *http.Request, title string, d any) *Page {
	p := &Page{Title: title, Layout: "app", D: d, Dev: s.app.Cfg.DevMode, Now: s.app.Now(), Path: r.URL.Path, Query: r.URL.Query()}
	if sess := sessionOf(r); sess != nil {
		p.User, p.CSRF = sess.User, sess.CSRF
		p.Unread = s.app.UnreadCount(r.Context(), sess.User.ID)
		switch {
		case sess.User.IsStaff():
			p.Area, p.Eyebrow = "ops", "Eimsky ops"
			if sess.User.IsAdmin() {
				p.Eyebrow = "Eimsky admin"
			}
			p.Pending = s.app.PendingApprovals(r.Context())
		case sess.User.IsFactory():
			p.Area, p.Eyebrow = "factory", "Eimsky · Shipper portal"
		case sess.User.IsViewer():
			p.Area, p.Eyebrow = "viewer", "Eimsky · Industry insights"
		}
	}
	return p
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, p *Page) {
	t, ok := s.pages[name]
	if !ok {
		s.app.Log.Error("missing template", "name", name)
		http.Error(w, "page not found", 500)
		return
	}
	if p.Flash == nil {
		p.Flash = s.takeFlash(w, r)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	layout := p.Layout
	if layout == "" {
		layout = "app"
	}
	if err := t.ExecuteTemplate(w, layout, p); err != nil {
		s.app.Log.Error("render", "template", name, "err", err)
	}
}

// fragment renders a named block from a page's template set (for htmx).
func (s *Server) fragment(w http.ResponseWriter, page, block string, p *Page) {
	t, ok := s.pages[page]
	if !ok {
		http.Error(w, "missing", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, block, p); err != nil {
		s.app.Log.Error("fragment", "page", page, "block", block, "err", err)
	}
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
	p := s.page(r, "Not found", nil)
	if p.User == nil {
		p.Layout = "auth"
	}
	s.render(w, r, "common/notfound", p)
}

// fail handles an error: friendly messages go back as a flash, others are logged.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error, back string) {
	msg, ok := app.IsUserError(err)
	if errors.Is(err, app.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if !ok {
		s.app.Log.Error("request failed", "path", r.URL.Path, "err", err)
		msg = "Something went wrong on our side. Please try again in a moment."
	}
	if r.Method == http.MethodGet && back == "" {
		w.WriteHeader(http.StatusInternalServerError)
		p := s.page(r, "Error", msg)
		s.render(w, r, "common/error", p)
		return
	}
	s.flash(w, "bad", msg)
	if back == "" {
		back = backURL(r, "/")
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

func (s *Server) done(w http.ResponseWriter, r *http.Request, msg, to string) {
	if msg != "" {
		s.flash(w, "good", msg)
	}
	if to == "" {
		to = backURL(r, "/")
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func backURL(r *http.Request, def string) string {
	ref := r.Header.Get("Referer")
	if ref == "" {
		return def
	}
	u, err := url.Parse(ref)
	if err != nil || (u.Host != "" && u.Host != r.Host) {
		return def
	}
	return u.RequestURI()
}

// safeNext only allows local paths.
func safeNext(n string) string {
	if n == "" || !strings.HasPrefix(n, "/") || strings.HasPrefix(n, "//") || strings.HasPrefix(n, "/\\") {
		return ""
	}
	return path.Clean(n[:strings.IndexAny(n+"?", "?")]) + n[strings.IndexAny(n+"?", "?"):]
}

func (s *Server) flash(w http.ResponseWriter, kind, msg string) {
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: url.QueryEscape(kind + "|" + msg), Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.app.Cfg.CookieSecure, MaxAge: 60})
}

func (s *Server) takeFlash(w http.ResponseWriter, r *http.Request) *Flash {
	c, err := r.Cookie(flashCookie)
	if err != nil {
		return nil
	}
	s.clearCookie(w, flashCookie)
	v, err := url.QueryUnescape(c.Value)
	if err != nil {
		return nil
	}
	kind, msg, ok := strings.Cut(v, "|")
	if !ok {
		return nil
	}
	return &Flash{Kind: kind, Msg: msg}
}

func (s *Server) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.app.Cfg.CookieSecure})
}

func (s *Server) setSession(w http.ResponseWriter, tok string) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.app.Cfg.CookieSecure, MaxAge: int(app.WebSessionTTL.Seconds())})
}

// ---------- Form helpers ----------

func idParam(r *http.Request, name string) int64 {
	v, _ := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	return v
}

func formInt(r *http.Request, name string) int64 {
	v, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue(name)), 10, 64)
	return v
}

func formFloat(r *http.Request, name string) float64 {
	v, _ := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(r.FormValue(name)), ",", ""), 64)
	return v
}

func parseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(s), ",", ""), 64)
	return v
}

func formTime(r *http.Request, name string) time.Time {
	t, err := time.ParseInLocation("2006-01-02T15:04", strings.TrimSpace(r.FormValue(name)), domain.Colombo)
	if err != nil {
		return time.Time{}
	}
	return t
}

func formInts(r *http.Request, name string) []int64 {
	var out []int64
	for _, v := range r.Form[name] {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			out = append(out, n)
		}
	}
	return out
}
