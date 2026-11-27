package web

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/itsmepava/container/internal/app"
)

func (s *Server) authRoutes(r chi.Router) {
	r.Get("/login", s.loginPage)
	r.Post("/login", s.loginPost)
	r.Post("/logout", s.logout)
	r.Get("/signup", s.signupPage)
	r.Post("/signup", s.signupPost)
	r.Get("/forgot", s.forgotPage)
	r.Post("/forgot", s.forgotPost)
	r.Get("/reset", s.linkPage("reset"))
	r.Post("/reset", s.linkPost("reset"))
	r.Get("/invite", s.linkPage("invite"))
	r.Post("/invite", s.linkPost("invite"))
}

func homeFor(u *app.User) string {
	switch {
	case u.IsStaff():
		return "/ops/pools"
	case u.IsFactory():
		if !u.Approved() {
			return "/f/bookings"
		}
		return "/f/book"
	case u.IsViewer():
		return "/insights"
	case u.IsForwarder():
		return "/account"
	}
	return "/account"
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, homeFor(userOf(r)), http.StatusSeeOther)
}

type loginData struct {
	Email string
	Next  string
	Demo  bool
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if u := userOf(r); u != nil {
		http.Redirect(w, r, homeFor(u), http.StatusSeeOther)
		return
	}
	p := s.page(r, "Sign in", loginData{Next: safeNext(r.URL.Query().Get("next")), Demo: s.app.Cfg.DevMode})
	p.Layout = "auth"
	s.render(w, r, "auth/login", p)
}

func (s *Server) loginPost(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.FormValue("email"))
	d := loginData{Email: email, Next: safeNext(r.FormValue("next")), Demo: s.app.Cfg.DevMode}
	show := func(msg string) {
		p := s.page(r, "Sign in", d)
		p.Layout, p.Flash = "auth", &Flash{Kind: "bad", Msg: msg}
		w.WriteHeader(http.StatusUnauthorized)
		s.render(w, r, "auth/login", p)
	}
	if !s.limiter.Allow("login:"+clientIP(r), 10, time.Minute) || !s.limiter.Allow("login:"+strings.ToLower(email), 8, 10*time.Minute) {
		show("Too many sign-in attempts. Wait a few minutes and try again.")
		return
	}
	u, err := s.app.Login(r.Context(), email, r.FormValue("password"))
	if err != nil {
		msg, ok := app.IsUserError(err)
		if !ok {
			s.app.Log.Error("login", "err", err)
			msg = "Something went wrong. Please try again."
		}
		show(msg)
		return
	}
	if u.IsForwarder() {
		show("Forwarders sign in with the Eimsky mobile app.")
		return
	}
	tok, err := s.app.CreateSession(r.Context(), u, "web", r.UserAgent())
	if err != nil {
		s.fail(w, r, err, "/login")
		return
	}
	s.setSession(w, tok)
	to := d.Next
	if to == "" {
		to = homeFor(u)
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.app.DeleteSession(r.Context(), c.Value)
	}
	s.clearCookie(w, sessionCookie)
	s.flash(w, "info", "You're signed out.")
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) signupPage(w http.ResponseWriter, r *http.Request) {
	p := s.page(r, "Register your factory", app.SignupInput{})
	p.Layout = "auth"
	s.render(w, r, "auth/signup", p)
}

func (s *Server) signupPost(w http.ResponseWriter, r *http.Request) {
	in := app.SignupInput{CompanyName: r.FormValue("company"), RegNumber: r.FormValue("reg"), Address: r.FormValue("address"),
		Name: r.FormValue("name"), Email: r.FormValue("email"), Phone: r.FormValue("phone"), Password: r.FormValue("password")}
	if !s.limiter.Allow("signup:"+clientIP(r), 5, time.Hour) {
		s.fail(w, r, app.Errf("Too many sign-ups from this network. Try again later."), "/signup")
		return
	}
	u, err := s.app.SignupFactory(r.Context(), in)
	if err != nil {
		msg, ok := app.IsUserError(err)
		if !ok {
			s.app.Log.Error("signup", "err", err)
			msg = "Something went wrong. Please try again."
		}
		in.Password = ""
		p := s.page(r, "Register your factory", in)
		p.Layout, p.Flash = "auth", &Flash{Kind: "bad", Msg: msg}
		w.WriteHeader(http.StatusUnprocessableEntity)
		s.render(w, r, "auth/signup", p)
		return
	}
	tok, err := s.app.CreateSession(r.Context(), u, "web", r.UserAgent())
	if err != nil {
		s.fail(w, r, err, "/login")
		return
	}
	s.setSession(w, tok)
	s.done(w, r, "Thanks! Eimsky will check your factory's details within one working day. You can look around in the meantime.", "/f/bookings")
}

func (s *Server) forgotPage(w http.ResponseWriter, r *http.Request) {
	p := s.page(r, "Reset password", nil)
	p.Layout = "auth"
	s.render(w, r, "auth/forgot", p)
}

func (s *Server) forgotPost(w http.ResponseWriter, r *http.Request) {
	if s.limiter.Allow("forgot:"+clientIP(r), 5, 15*time.Minute) {
		if err := s.app.RequestPasswordReset(r.Context(), r.FormValue("email")); err != nil {
			s.app.Log.Error("reset", "err", err)
		}
	}
	s.flash(w, "info", "If that email has an account, a reset link is on its way. It works for 2 hours.")
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

type linkData struct {
	Kind  string
	Token string
	User  *app.User
	Err   string
}

func (s *Server) linkPage(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := r.URL.Query().Get("token")
		d := linkData{Kind: kind, Token: tok}
		u, err := s.app.LinkUser(r.Context(), kind, tok)
		if err != nil {
			d.Err, _ = app.IsUserError(err)
		}
		d.User = u
		title := "Choose a new password"
		if kind == "invite" {
			title = "Set up your account"
		}
		p := s.page(r, title, d)
		p.Layout = "auth"
		s.render(w, r, "auth/link", p)
	}
}

func (s *Server) linkPost(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := r.FormValue("token")
		pw := r.FormValue("password")
		if pw != r.FormValue("confirm") {
			s.fail(w, r, app.Errf("The two passwords don't match."), "/"+kind+"?token="+tok)
			return
		}
		u, err := s.app.SetPasswordWithLink(r.Context(), kind, tok, pw)
		if err != nil {
			s.fail(w, r, err, "/"+kind+"?token="+tok)
			return
		}
		if u.IsForwarder() {
			s.done(w, r, "Password set.", "/login")
			return
		}
		t, err := s.app.CreateSession(r.Context(), u, "web", r.UserAgent())
		if err != nil {
			s.fail(w, r, err, "/login")
			return
		}
		s.setSession(w, t)
		s.done(w, r, "You're all set. Welcome to Eimsky.", homeFor(u))
	}
}
