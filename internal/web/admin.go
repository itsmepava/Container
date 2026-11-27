package web

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/itsmepava/container/internal/app"
	"github.com/itsmepava/container/internal/domain"
)

func (s *Server) adminRoutes(r chi.Router) {
	r.Get("/", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/admin/lanes", http.StatusSeeOther) })
	r.Get("/lanes", s.lanesPage)
	r.Get("/lanes/new", s.laneForm)
	r.Get("/lanes/{id}", s.laneForm)
	r.Post("/lanes", s.laneSave)
	r.Get("/sailings/new", s.sailingNew)
	r.Post("/sailings", s.sailingCreate)
	r.Get("/sailings/{id}", s.sailingPage)
	r.Post("/sailings/{id}/times", s.sailingTimes)
	r.Post("/sailings/{id}/vessels", s.sailingVessel)
	r.Post("/sailings/{id}/copy", s.sailingCopy)
	r.Get("/companies", s.companiesPage)
	r.Post("/companies", s.companyCreate)
	r.Get("/companies/{id}", s.companyPage)
	r.Post("/companies/{id}/status", s.companyStatus)
	r.Post("/companies/{id}/docs", s.companyUpload)
	r.Get("/users", s.usersPage)
	r.Post("/users/invite", s.userInvite)
	r.Post("/users/{id}/active", s.userActive)
	r.Get("/settings", s.settingsPage)
	r.Post("/settings", s.settingsSave)
	r.Post("/container-types", s.containerTypeSave)
	r.Post("/ports", s.portSave)
	r.Post("/hs-import", s.hsImport)
	r.Get("/audit", s.auditPage)
}

func (s *Server) adminOnly(w http.ResponseWriter, r *http.Request) bool {
	if !userOf(r).IsAdmin() {
		s.fail(w, r, app.Errf("Only Eimsky admins can change this."), "/admin/lanes")
		return false
	}
	return true
}

// ---------- Lanes and sailings ----------

type laneRow struct {
	L        *app.Lane
	Sailings []*app.Sailing
}

func (s *Server) lanesPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	lanes, err := s.app.Lanes(ctx, false)
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	var rows []laneRow
	for _, l := range lanes {
		ss, err := s.app.Sailings(ctx, app.SailingFilter{LaneID: l.ID, FromWeek: s.app.Now().AddDate(0, 0, -7)})
		if err != nil {
			s.fail(w, r, err, "")
			return
		}
		rows = append(rows, laneRow{L: l, Sailings: ss})
	}
	specs, _ := s.app.ContainerSpecs(ctx)
	s.render(w, r, "admin/lanes", s.page(r, "Lanes and sailings", map[string]any{"Rows": rows, "Specs": specs}))
}

func (s *Server) laneForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ports, _ := s.app.Ports(ctx)
	specs, _ := s.app.ContainerSpecs(ctx)
	l := &app.Lane{Active: true, ColoadRate: 55, EstimatePerWM: 50, DefaultBox: "40HC", DefaultGrade: domain.GradeA, Rates: map[string]float64{}, Fallback: map[string]float64{}, Dest: app.Port{Code: "LKCMB"}}
	if id := idParam(r, "id"); id != 0 {
		var err error
		if l, err = s.app.LaneByID(ctx, id); err != nil {
			s.fail(w, r, err, "")
			return
		}
	}
	title := "New lane"
	if l.ID != 0 {
		title = "Edit " + l.Short()
	}
	fws, _ := s.app.Companies(ctx, "forwarder", "approved", "")
	s.render(w, r, "admin/lane", s.page(r, title, map[string]any{"L": l, "Ports": ports, "Specs": specs, "Forwarders": fws}))
}

func (s *Server) laneSave(w http.ResponseWriter, r *http.Request) {
	specs, _ := s.app.ContainerSpecs(r.Context())
	in := app.LaneInput{ID: formInt(r, "id"), Origin: r.FormValue("origin"), Dest: r.FormValue("dest"), Active: r.FormValue("active") == "on",
		ColoadRate: formFloat(r, "coload_rate"), EstimatePerWM: formFloat(r, "estimate"), OriginCFS: r.FormValue("origin_cfs"),
		DefaultBox: r.FormValue("default_box"), DefaultGrade: r.FormValue("default_grade"), Rates: map[string]float64{}}
	in.Fallback = map[string]float64{}
	in.FallbackID = formInt(r, "fallback_company")
	for _, sp := range specs {
		if v := formFloat(r, "rate_"+sp.Code); v > 0 {
			in.Rates[sp.Code] = v
		}
		if v := formFloat(r, "fallback_"+sp.Code); v > 0 {
			in.Fallback[sp.Code] = v
		}
	}
	back := "/admin/lanes/new"
	if in.ID != 0 {
		back = fmt.Sprintf("/admin/lanes/%d", in.ID)
	}
	if _, err := s.app.SaveLane(r.Context(), userOf(r), in); err != nil {
		s.fail(w, r, err, back)
		return
	}
	s.done(w, r, "Lane saved.", "/admin/lanes")
}

type sailingForm struct {
	Lanes         []*app.Lane
	LaneID        int64
	Year          int
	Week          int
	Cutoff        time.Time
	Lock          time.Time
	Close         time.Time
	VA, VB        app.VesselInput
	DecisionHours int
	BidCloseHours int
}

func (s *Server) sailingNew(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	lanes, _ := s.app.Lanes(ctx, true)
	f := sailingForm{Lanes: lanes, LaneID: parseInt64(r.URL.Query().Get("lane"))}
	if f.LaneID == 0 && len(lanes) > 0 {
		f.LaneID = lanes[0].ID
	}
	// Suggest the first week without a sailing, starting next week.
	y, wk := domain.ISOWeek(s.app.Now().AddDate(0, 0, 7))
	for i := 0; i < 20; i++ {
		ss, _ := s.app.Sailings(ctx, app.SailingFilter{LaneID: f.LaneID, Year: y, Week: wk})
		if len(ss) == 0 {
			break
		}
		y, wk = domain.ISOWeek(domain.WeekStart(y, wk).AddDate(0, 0, 7))
	}
	f.Year, f.Week = y, wk
	mon := domain.WeekStart(y, wk)
	at := func(t time.Time, h int) time.Time {
		return time.Date(t.Year(), t.Month(), t.Day(), h, 0, 0, 0, domain.Colombo)
	}
	st := s.app.Settings(ctx)
	f.Cutoff = at(mon, 17)
	f.Lock = f.Cutoff.Add(-time.Duration(st.DecisionHours) * time.Hour)
	f.Close = f.Cutoff.Add(-time.Duration(st.BidCloseHours) * time.Hour)
	f.DecisionHours, f.BidCloseHours = st.DecisionHours, st.BidCloseHours
	f.VA = app.VesselInput{Service: "standard", ETD: at(mon.AddDate(0, 0, 3), 18), ETA: at(mon.AddDate(0, 0, 12), 8), IsDefault: true}
	f.VB = app.VesselInput{Service: "fast", ETD: at(mon.AddDate(0, 0, 2), 20), ETA: at(mon.AddDate(0, 0, 8), 8)}
	s.render(w, r, "admin/sailing_new", s.page(r, "New sailing", f))
}

func vesselFromForm(r *http.Request, p string) app.VesselInput {
	return app.VesselInput{Name: r.FormValue(p + "name"), Voyage: r.FormValue(p + "voyage"), Service: r.FormValue(p + "service"), Via: r.FormValue(p + "via"),
		ETD: formTime(r, p+"etd"), ETA: formTime(r, p+"eta"), ExtraPerBox: formFloat(r, p+"extra")}
}

func (s *Server) sailingCreate(w http.ResponseWriter, r *http.Request) {
	week := int(formInt(r, "week"))
	year := int(formInt(r, "year"))
	in := app.SailingInput{LaneID: formInt(r, "lane"), Year: year, Week: week, CFSCutoff: formTime(r, "cutoff"), PlanLockAt: formTime(r, "lock"), BidCloseAt: formTime(r, "close")}
	a := vesselFromForm(r, "a_")
	a.IsDefault = true
	in.Vessels = append(in.Vessels, a)
	if b := vesselFromForm(r, "b_"); strings.TrimSpace(b.Name) != "" {
		in.Vessels = append(in.Vessels, b)
	}
	id, err := s.app.CreateSailing(r.Context(), userOf(r), in)
	if err != nil {
		s.fail(w, r, err, fmt.Sprintf("/admin/sailings/new?lane=%d", in.LaneID))
		return
	}
	s.done(w, r, fmt.Sprintf("Week %d is open for booking.", week), fmt.Sprintf("/admin/sailings/%d", id))
}

func (s *Server) sailingPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sl, err := s.app.SailingByID(ctx, idParam(r, "id"))
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	cs, _ := s.app.Containers(ctx, s.app.DB, sl.ID)
	audit, _ := s.app.AuditList(ctx, "sailing", sl.ID, "", 30)
	s.render(w, r, "admin/sailing", s.page(r, sl.Lane.Short()+" · "+sl.Label(), map[string]any{"S": sl, "Containers": cs, "Audit": audit}))
}

func (s *Server) sailingTimes(w http.ResponseWriter, r *http.Request) {
	id := idParam(r, "id")
	if err := s.app.UpdateSailingTimes(r.Context(), userOf(r), id, formTime(r, "lock"), formTime(r, "close"), formTime(r, "cutoff")); err != nil {
		s.fail(w, r, err, fmt.Sprintf("/admin/sailings/%d", id))
		return
	}
	s.done(w, r, "Deadlines saved.", fmt.Sprintf("/admin/sailings/%d", id))
}

func (s *Server) sailingVessel(w http.ResponseWriter, r *http.Request) {
	id := idParam(r, "id")
	if err := s.app.AddVessel(r.Context(), userOf(r), id, vesselFromForm(r, "")); err != nil {
		s.fail(w, r, err, fmt.Sprintf("/admin/sailings/%d", id))
		return
	}
	s.done(w, r, "Vessel added.", fmt.Sprintf("/admin/sailings/%d", id))
}

func (s *Server) sailingCopy(w http.ResponseWriter, r *http.Request) {
	nid, err := s.app.CopySailingToNextWeek(r.Context(), userOf(r), idParam(r, "id"))
	if err != nil {
		s.fail(w, r, err, "/admin/lanes")
		return
	}
	s.done(w, r, "Next week created with the same schedule. Check the vessels and times.", fmt.Sprintf("/admin/sailings/%d", nid))
}

// ---------- Companies ----------

func (s *Server) companiesPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	cs, err := s.app.Companies(r.Context(), q.Get("kind"), q.Get("status"), q.Get("q"))
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	s.render(w, r, "admin/companies", s.page(r, "Companies", map[string]any{"Companies": cs, "Kind": q.Get("kind"), "Status": q.Get("status"), "Q": q.Get("q")}))
}

func (s *Server) companyCreate(w http.ResponseWriter, r *http.Request) {
	id, _, err := s.app.CreateCompany(r.Context(), userOf(r), r.FormValue("kind"), r.FormValue("name"), r.FormValue("reg"), r.FormValue("contact"), r.FormValue("email"))
	if err != nil {
		s.fail(w, r, err, "/admin/companies")
		return
	}
	s.done(w, r, "Company added and its first user invited by email.", fmt.Sprintf("/admin/companies/%d", id))
}

func (s *Server) companyPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, err := s.app.CompanyByID(ctx, idParam(r, "id"))
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	d := map[string]any{"C": c}
	d["Users"], _ = s.app.UsersWhere(ctx, "u.company_id=$1", c.ID)
	d["Docs"], _ = s.app.DocsFor(ctx, "company", c.ID)
	d["Lanes"], _ = s.app.Lanes(ctx, false)
	d["Audit"], _ = s.app.AuditList(ctx, "company", c.ID, "", 30)
	if c.Kind == "factory" {
		d["Bookings"], _ = s.app.CompanyBookings(ctx, c.ID, true)
	}
	s.render(w, r, "admin/company", s.page(r, c.Name, d))
}

func (s *Server) companyStatus(w http.ResponseWriter, r *http.Request) {
	id := idParam(r, "id")
	st := r.FormValue("status")
	if err := s.app.SetCompanyStatus(r.Context(), userOf(r), id, st, r.FormValue("note")); err != nil {
		s.fail(w, r, err, fmt.Sprintf("/admin/companies/%d", id))
		return
	}
	s.done(w, r, "Status changed to "+st+". The company has been told.", fmt.Sprintf("/admin/companies/%d", id))
}

func (s *Server) companyUpload(w http.ResponseWriter, r *http.Request) {
	id := idParam(r, "id")
	if err := s.uploadFromForm(r, "company", id); err != nil {
		s.fail(w, r, err, fmt.Sprintf("/admin/companies/%d", id))
		return
	}
	s.done(w, r, "Document uploaded.", fmt.Sprintf("/admin/companies/%d", id))
}

// ---------- Users ----------

func (s *Server) usersPage(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	where := "u.company_id IS NULL"
	var args []any
	if q != "" {
		where = "(u.name ILIKE '%'||$1||'%' OR u.email ILIKE '%'||$1||'%' OR u.phone ILIKE '%'||$1||'%' OR c.name ILIKE '%'||$1||'%')"
		args = append(args, q)
	}
	us, err := s.app.UsersWhere(r.Context(), where, args...)
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	assoc, _ := s.app.Companies(r.Context(), "association", "", "")
	s.render(w, r, "admin/users", s.page(r, "Users", map[string]any{"Users": us, "Q": q, "Assoc": assoc}))
}

func (s *Server) userInvite(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	role := app.Role(r.FormValue("role"))
	var cid int64
	if role == app.RoleViewer {
		cid = formInt(r, "company")
		if cid == 0 {
			s.fail(w, r, app.Errf("Choose the association the viewer belongs to."), "/admin/users")
			return
		}
	} else if role != app.RoleOps && role != app.RoleAdmin {
		s.fail(w, r, app.Errf("Choose a role."), "/admin/users")
		return
	}
	if _, _, err := s.app.InviteUser(r.Context(), userOf(r), cid, role, r.FormValue("name"), r.FormValue("email")); err != nil {
		s.fail(w, r, err, "/admin/users")
		return
	}
	s.done(w, r, "Invitation sent.", "/admin/users")
}

func (s *Server) userActive(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	if err := s.app.SetUserActive(r.Context(), userOf(r), idParam(r, "id"), r.FormValue("active") == "1"); err != nil {
		s.fail(w, r, err, "")
		return
	}
	s.done(w, r, "Saved.", "")
}

// ---------- Settings ----------

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	m, _ := s.app.SettingsMap(ctx)
	specs, _ := s.app.ContainerSpecs(ctx)
	ports, _ := s.app.Ports(ctx)
	s.render(w, r, "admin/settings", s.page(r, "Settings", map[string]any{"Values": m, "Keys": app.SettingKeys, "Specs": specs, "Ports": ports, "Stowage": s.app.Settings(ctx).Stowage, "HSCount": s.app.HSCount(ctx)}))
}

func (s *Server) settingsSave(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	vals := map[string]string{}
	for _, k := range app.SettingKeys {
		vals[k.Key] = r.FormValue(k.Key)
	}
	if err := s.app.SaveSettings(r.Context(), userOf(r), vals); err != nil {
		s.fail(w, r, err, "/admin/settings")
		return
	}
	s.done(w, r, "Settings saved.", "/admin/settings")
}

func (s *Server) containerTypeSave(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	sp := domain.ContainerSpec{Code: r.FormValue("code"), Name: r.FormValue("name"), InternalCBM: formFloat(r, "internal"), PayloadKg: formFloat(r, "payload")}
	if err := s.app.SaveContainerType(r.Context(), userOf(r), sp, r.FormValue("active") != "0"); err != nil {
		s.fail(w, r, err, "/admin/settings")
		return
	}
	s.done(w, r, "Container type saved.", "/admin/settings")
}

func (s *Server) portSave(w http.ResponseWriter, r *http.Request) {
	if err := s.app.SavePort(r.Context(), userOf(r), app.Port{Code: r.FormValue("code"), Name: r.FormValue("name"), Country: r.FormValue("country")}); err != nil {
		s.fail(w, r, err, "/admin/settings")
		return
	}
	s.done(w, r, "Port saved.", "/admin/settings")
}

func (s *Server) auditPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	es, err := s.app.AuditList(r.Context(), "", 0, q, 300)
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	s.render(w, r, "admin/audit", s.page(r, "Audit log", map[string]any{"Entries": es, "Q": q}))
}

// ---------- Insights and dev ----------

func (s *Server) insights(w http.ResponseWriter, r *http.Request) {
	weeks := int(parseInt64(r.URL.Query().Get("weeks")))
	if weeks != 4 && weeks != 8 && weeks != 13 && weeks != 26 {
		weeks = 8
	}
	in, err := s.app.Insights(r.Context(), weeks)
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	s.render(w, r, "common/insights", s.page(r, "Inbound raw materials", in))
}

func (s *Server) devMail(w http.ResponseWriter, r *http.Request) {
	es, err := s.app.Outbox(r.Context(), 100)
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	s.render(w, r, "admin/devmail", s.page(r, "Email outbox", es))
}

func (s *Server) hsImport(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	if err := r.ParseMultipartForm(12 << 20); err != nil {
		s.fail(w, r, app.Errf("That file is too large."), "/admin/settings")
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		s.fail(w, r, app.Errf("Choose a CSV file to import."), "/admin/settings")
		return
	}
	defer f.Close()
	cr := csv.NewReader(f)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	rows, err := cr.ReadAll()
	if err != nil {
		s.fail(w, r, app.Errf("We couldn't read that CSV: %s", err.Error()), "/admin/settings")
		return
	}
	added, skipped, err := s.app.ImportHSCodes(r.Context(), userOf(r), rows)
	if err != nil {
		s.fail(w, r, err, "/admin/settings")
		return
	}
	msg := fmt.Sprintf("Imported %d HS codes.", added)
	if skipped > 0 {
		msg = fmt.Sprintf("Imported %d HS codes; %d rows skipped (header or invalid code).", added, skipped)
	}
	s.done(w, r, msg, "/admin/settings")
}
