package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/itsmepava/container/internal/app"
)

func (s *Server) commonRoutes(r chi.Router) {
	r.Get("/notifications", s.notifications)
	r.Get("/notifications/count", s.notificationCount)
	r.Post("/notifications/read", s.notificationsRead)
	r.Get("/notifications/{id}", s.notificationOpen)
	r.Get("/account", s.account)
	r.Post("/account/profile", s.accountProfile)
	r.Post("/account/password", s.accountPassword)
	r.Post("/account/notify", s.accountNotify)
	r.Post("/account/company", s.accountCompany)
	r.Get("/docs/{id}", s.docDownload)
	r.Get("/hs", s.hsSearch)
}

func (s *Server) notifications(w http.ResponseWriter, r *http.Request) {
	ns, err := s.app.Notifications(r.Context(), userOf(r).ID, 100)
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	p := s.page(r, "Notifications", ns)
	p.Layout = "app"
	s.render(w, r, "common/notifications", p)
}

func (s *Server) notificationCount(w http.ResponseWriter, r *http.Request) {
	n := s.app.UnreadCount(r.Context(), userOf(r).ID)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	attrs := `hx-get="/notifications/count" hx-trigger="every 30s" hx-swap="outerHTML"`
	if n > 0 {
		label := fmt.Sprint(n)
		if n > 99 {
			label = "99+"
		}
		fmt.Fprintf(w, `<span %s><span class="badge-count">%s</span></span>`, attrs, label)
		return
	}
	fmt.Fprintf(w, `<span %s></span>`, attrs)
}

func (s *Server) notificationsRead(w http.ResponseWriter, r *http.Request) {
	_ = s.app.MarkRead(r.Context(), userOf(r).ID, formInt(r, "id"))
	s.done(w, r, "", "/notifications")
}

// notificationOpen marks a notification read and follows its link.
func (s *Server) notificationOpen(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	id := idParam(r, "id")
	ns, _ := s.app.Notifications(r.Context(), u.ID, 200)
	_ = s.app.MarkRead(r.Context(), u.ID, id)
	for _, n := range ns {
		if n.ID == id && safeNext(n.Link) != "" {
			http.Redirect(w, r, n.Link, http.StatusSeeOther)
			return
		}
	}
	http.Redirect(w, r, "/notifications", http.StatusSeeOther)
}

type accountData struct {
	Company *app.Company
}

func (s *Server) account(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	d := accountData{}
	if u.CompanyID != 0 {
		d.Company, _ = s.app.CompanyByID(r.Context(), u.CompanyID)
	}
	s.render(w, r, "common/account", s.page(r, "Your account", d))
}

func (s *Server) accountProfile(w http.ResponseWriter, r *http.Request) {
	if err := s.app.UpdateProfile(r.Context(), userOf(r), r.FormValue("name"), r.FormValue("phone")); err != nil {
		s.fail(w, r, err, "/account")
		return
	}
	s.done(w, r, "Profile saved.", "/account")
}

func (s *Server) accountPassword(w http.ResponseWriter, r *http.Request) {
	if r.FormValue("next") != r.FormValue("confirm") {
		s.fail(w, r, app.Errf("The two new passwords don't match."), "/account")
		return
	}
	if err := s.app.ChangePassword(r.Context(), userOf(r), r.FormValue("current"), r.FormValue("next")); err != nil {
		s.fail(w, r, err, "/account")
		return
	}
	s.done(w, r, "Password changed.", "/account")
}

func (s *Server) accountNotify(w http.ResponseWriter, r *http.Request) {
	on := r.FormValue("email_notify") == "on"
	if err := s.app.SetEmailNotify(r.Context(), userOf(r).ID, on); err != nil {
		s.fail(w, r, err, "/account")
		return
	}
	msg := "Email notifications turned off. You'll still see them here in Eimsky."
	if on {
		msg = "Email notifications turned on."
	}
	s.done(w, r, msg, "/account")
}

func (s *Server) accountCompany(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	if err := s.app.UpdateCompanyProfile(r.Context(), u, u.CompanyID, r.FormValue("name"), r.FormValue("reg"), r.FormValue("address"), r.FormValue("phone")); err != nil {
		s.fail(w, r, err, "/account")
		return
	}
	s.done(w, r, "Company details saved.", "/account")
}

func (s *Server) docDownload(w http.ResponseWriter, r *http.Request) {
	d, data, err := s.app.OpenDoc(r.Context(), userOf(r), idParam(r, "id"))
	if err != nil {
		if msg, ok := app.IsUserError(err); ok && err != app.ErrNotFound {
			http.Error(w, msg, http.StatusGone)
			return
		}
		s.notFound(w, r)
		return
	}
	serveFile(w, d.Filename, d.ContentType, data, r.URL.Query().Get("download") == "1")
}

func serveFile(w http.ResponseWriter, name, ct string, data []byte, attach bool) {
	disp := "inline"
	if attach {
		disp = "attachment"
	}
	safe := strings.Map(func(r rune) rune {
		if r < 32 || r == '"' || r == '\\' || r > 126 {
			return '_'
		}
		return r
	}, name)
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename="%s"`, disp, safe))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	w.Write(data)
}

func (s *Server) hsSearch(w http.ResponseWriter, r *http.Request) {
	hs, err := s.app.SearchHS(r.Context(), r.URL.Query().Get("q"), 8)
	if err != nil {
		hs = nil
	}
	out := []map[string]string{}
	for _, h := range hs {
		out = append(out, map[string]string{"code": h.Code, "description": h.Description})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
