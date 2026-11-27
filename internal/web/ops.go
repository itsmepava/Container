package web

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/itsmepava/container/internal/app"
	"github.com/itsmepava/container/internal/domain"
)

func (s *Server) opsRoutes(r chi.Router) {
	r.Get("/", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/ops/pools", http.StatusSeeOther) })
	r.Get("/pools", s.poolsPage)
	r.Post("/sailings/{id}/decide", s.decide)
	r.Get("/plan", s.planIndex)
	r.Get("/sailings/{id}/plan", s.planPage)
	r.Post("/sailings/{id}/vessel", s.planVessel)
	r.Post("/sailings/{id}/autoplace", s.planAutoplace)
	r.Post("/sailings/{id}/containers", s.planAddContainer)
	r.Post("/sailings/{id}/lock", s.planLock)
	r.Post("/containers/{id}", s.planUpdateContainer)
	r.Post("/containers/{id}/remove", s.planRemoveContainer)
	r.Post("/bookings/{id}/place", s.planPlace)
	r.Post("/bookings/{id}/move", s.planMove)
	r.Get("/tenders", s.tendersPage)
	r.Get("/tenders/{id}", s.tenderPage)
	r.Post("/tenders/{id}/award", s.tenderAward)
	r.Post("/tenders/{id}/extend", s.tenderExtend)
	r.Post("/tenders/{id}/close", s.tenderClose)
	r.Post("/tenders/{id}/invite", s.tenderInvite)
	r.Post("/tenders/{id}/docs", s.tenderUpload)
	r.Get("/bookings", s.opsBookings)
	r.Get("/bookings/{id}", s.opsBooking)
	r.Post("/bookings/{id}/docs", s.opsBookingUpload)
	r.Post("/bookings/{id}/cancel", s.opsBookingCancel)
	r.Get("/invoices", s.opsInvoices)
	r.Get("/invoices/{id}.pdf", s.opsInvoicePDF)
	r.Post("/invoices/{id}/paid", s.opsInvoicePaid)
}

// ---------- Pools ----------

type poolsData struct {
	Year, Week   int
	PrevY, PrevW int
	NextY, NextW int
	Cards        []app.PoolCard
	Selected     *app.PoolCard
	Plan         *app.Planner
	NextDecision *app.PoolCard
}

func (s *Server) poolsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	y, wk := domain.ISOWeek(s.app.Now().AddDate(0, 0, 7))
	if v, err := strconv.Atoi(q.Get("week")); err == nil && v > 0 {
		wk = v
		if yy, err := strconv.Atoi(q.Get("year")); err == nil {
			y = yy
		}
	}
	d := poolsData{Year: y, Week: wk}
	d.PrevY, d.PrevW = domain.ISOWeek(domain.WeekStart(y, wk).AddDate(0, 0, -7))
	d.NextY, d.NextW = domain.ISOWeek(domain.WeekStart(y, wk).AddDate(0, 0, 7))
	cards, err := s.app.PoolsBoard(ctx, y, wk)
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	d.Cards = cards
	sel := parseInt64(q.Get("s"))
	for i := range d.Cards {
		c := &d.Cards[i]
		if c.Sailing.ID == sel {
			d.Selected = c
		}
		if c.Sailing.Status == "open" && (d.NextDecision == nil || c.Sailing.PlanLockAt.Before(d.NextDecision.Sailing.PlanLockAt)) {
			d.NextDecision = c
		}
	}
	if d.Selected == nil {
		for i := range d.Cards {
			if d.Cards[i].Badge == "DECIDE" {
				d.Selected = &d.Cards[i]
				break
			}
		}
	}
	if d.Selected == nil && len(d.Cards) > 0 {
		d.Selected = &d.Cards[0]
	}
	if d.Selected != nil {
		if d.Plan, err = s.app.Planner(ctx, d.Selected.Sailing.ID); err != nil {
			s.fail(w, r, err, "")
			return
		}
	}
	p := s.page(r, fmt.Sprintf("Import lane pools · Week %d", wk), d)
	s.render(w, r, "ops/pools", p)
}

func (s *Server) decide(w http.ResponseWriter, r *http.Request) {
	id := idParam(r, "id")
	opt := r.FormValue("option")
	if err := s.app.Decide(r.Context(), userOf(r), id, opt); err != nil {
		s.fail(w, r, err, "")
		return
	}
	switch opt {
	case "coload":
		s.done(w, r, "Every lot on this sailing goes with the co-load partner. Shippers have been told their cost.", "")
	case "hybrid":
		s.done(w, r, "Switched to a smaller box; lots that didn't fit are co-loaded. Review the plan, then lock it.", fmt.Sprintf("/ops/sailings/%d/plan", id))
	default:
		s.done(w, r, "Own box it is. Finish the plan and lock it to open bidding.", fmt.Sprintf("/ops/sailings/%d/plan", id))
	}
}

// ---------- Load planner ----------

func (s *Server) planIndex(w http.ResponseWriter, r *http.Request) {
	f := SailingOpen(s.app.Now())
	f.LaneID = parseInt64(r.URL.Query().Get("lane"))
	ss, err := s.app.Sailings(r.Context(), f)
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	var best *app.Sailing
	for _, x := range ss {
		if x.Status == "open" && (best == nil || x.PlanLockAt.Before(best.PlanLockAt)) {
			best = x
		}
	}
	if best == nil && len(ss) > 0 {
		best = ss[len(ss)-1]
	}
	if best == nil {
		s.done(w, r, "", "/admin/lanes")
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/ops/sailings/%d/plan", best.ID), http.StatusSeeOther)
}

func SailingOpen(now time.Time) app.SailingFilter {
	return app.SailingFilter{FromWeek: now.AddDate(0, 0, -14)}
}

type planData struct {
	P        *app.Planner
	Lanes    []*app.Lane
	Sailings []*app.Sailing
	NextPlan *app.Planner
	MoveLot  *app.PlannedLot
	Locked   bool
	Specs    []domain.ContainerSpec
	Stowage  float64
}

func (s *Server) planPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := idParam(r, "id")
	p, err := s.app.Planner(ctx, id)
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	d := planData{P: p, Locked: p.Sailing.Status != "open", Specs: p.Specs, Stowage: s.app.Settings(ctx).Stowage}
	d.Lanes, _ = s.app.Lanes(ctx, false)
	d.Sailings, _ = s.app.Sailings(ctx, app.SailingFilter{LaneID: p.Sailing.Lane.ID, FromWeek: s.app.Now().AddDate(0, 0, -28)})
	if p.NextSailing != nil {
		d.NextPlan, _ = s.app.Planner(ctx, p.NextSailing.ID)
	}
	lot := parseInt64(r.URL.Query().Get("lot"))
	for i := range p.All {
		if p.All[i].Booking.ID == lot {
			d.MoveLot = &p.All[i]
		}
	}
	if d.MoveLot == nil && len(p.Unplaced) > 0 {
		d.MoveLot = &p.Unplaced[0]
	}
	title := fmt.Sprintf("Plan · %s · %s", p.Sailing.Lane.Short(), p.Sailing.Label())
	s.render(w, r, "ops/plan", s.page(r, title, d))
}

func planURL(id int64) string { return fmt.Sprintf("/ops/sailings/%d/plan", id) }

func (s *Server) planVessel(w http.ResponseWriter, r *http.Request) {
	id := idParam(r, "id")
	if err := s.app.ChooseVessel(r.Context(), userOf(r), id, formInt(r, "vessel")); err != nil {
		s.fail(w, r, err, planURL(id))
		return
	}
	s.done(w, r, "Vessel updated.", planURL(id))
}

func (s *Server) planAutoplace(w http.ResponseWriter, r *http.Request) {
	id := idParam(r, "id")
	n, err := s.app.AutoPlace(r.Context(), userOf(r), id)
	if err != nil {
		s.fail(w, r, err, planURL(id))
		return
	}
	s.done(w, r, fmt.Sprintf("Placed %d lot(s) in booking order.", n), planURL(id))
}

func (s *Server) planAddContainer(w http.ResponseWriter, r *http.Request) {
	id := idParam(r, "id")
	if err := s.app.AddContainer(r.Context(), userOf(r), id, r.FormValue("box"), domain.Grade(r.FormValue("grade"))); err != nil {
		s.fail(w, r, err, planURL(id))
		return
	}
	s.done(w, r, "Container added. Place lots in it from the requests list, or use Auto-place.", planURL(id))
}

func (s *Server) planUpdateContainer(w http.ResponseWriter, r *http.Request) {
	c, err := s.app.ContainerByID(r.Context(), idParam(r, "id"))
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	box, grade := r.FormValue("box"), domain.Grade(r.FormValue("grade"))
	if box == "" {
		box = c.BoxCode
	}
	if grade == "" {
		grade = c.Grade
	}
	if err := s.app.UpdateContainer(r.Context(), userOf(r), c.ID, box, grade); err != nil {
		s.fail(w, r, err, planURL(c.SailingID))
		return
	}
	s.done(w, r, fmt.Sprintf("Container %d is now a %s, grade %s.", c.Seq, box, grade), planURL(c.SailingID))
}

func (s *Server) planRemoveContainer(w http.ResponseWriter, r *http.Request) {
	c, err := s.app.ContainerByID(r.Context(), idParam(r, "id"))
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	if err := s.app.RemoveContainer(r.Context(), userOf(r), c.ID); err != nil {
		s.fail(w, r, err, planURL(c.SailingID))
		return
	}
	s.done(w, r, "Container removed; its lots are waiting to be placed.", planURL(c.SailingID))
}

func (s *Server) planPlace(w http.ResponseWriter, r *http.Request) {
	if err := s.app.PlaceLot(r.Context(), userOf(r), idParam(r, "id"), formInt(r, "container")); err != nil {
		s.fail(w, r, err, "")
		return
	}
	s.done(w, r, "Lot moved.", "")
}

func (s *Server) planMove(w http.ResponseWriter, r *http.Request) {
	in := app.MoveInput{BookingID: idParam(r, "id"), Target: r.FormValue("target"), BoxCode: r.FormValue("box"), Reason: r.FormValue("reason"), AskShipper: r.FormValue("ask") == "on"}
	if err := s.app.RequestMove(r.Context(), userOf(r), in); err != nil {
		s.fail(w, r, err, "")
		return
	}
	msg := "Lot moved and the shipper told."
	if in.AskShipper {
		msg = "Request sent. The shipper sees it on their booking and by email."
	}
	s.done(w, r, msg, "")
}

func (s *Server) planLock(w http.ResponseWriter, r *http.Request) {
	id := idParam(r, "id")
	ids, err := s.app.LockAndTender(r.Context(), userOf(r), id)
	if err != nil {
		s.fail(w, r, err, planURL(id))
		return
	}
	if len(ids) == 1 {
		s.done(w, r, "Plan locked. Forwarders on this lane have been notified and can bid now.", fmt.Sprintf("/ops/tenders/%d", ids[0]))
		return
	}
	s.done(w, r, fmt.Sprintf("Plan locked. %d tenders are open for bidding.", len(ids)), "/ops/tenders?status=open")
}

// ---------- Tenders ----------

func (s *Server) tendersPage(w http.ResponseWriter, r *http.Request) {
	st := r.URL.Query().Get("status")
	if st == "" && r.URL.Query().Get("all") == "" {
		st = ""
	}
	ts, err := s.app.TenderList(r.Context(), st)
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	s.render(w, r, "ops/tenders", s.page(r, "Forwarder bids", map[string]any{"Tenders": ts, "Status": st}))
}

type tenderData struct {
	V          *app.TenderView
	Forwarders []*app.Company
	Docs       []*app.Document
	Missing    map[int64][]string
}

func (s *Server) tenderPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := idParam(r, "id")
	v, err := s.app.TenderView(ctx, id, parseInt64(r.URL.Query().Get("bid")))
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	d := tenderData{V: v}
	all, _ := s.app.Companies(ctx, "forwarder", "approved", "")
	invited := map[string]bool{}
	for _, n := range v.Invited {
		invited[n] = true
	}
	for _, c := range all {
		if !invited[c.Name] {
			d.Forwarders = append(d.Forwarders, c)
		}
	}
	d.Docs, _ = s.app.DocsFor(ctx, "tender", id)
	var bs []*app.Booking
	for _, b := range v.Bills {
		bs = append(bs, &app.Booking{ID: b.BookingID})
	}
	// MissingDocs needs category; reload bookings.
	if full, err := s.app.SearchBookings(ctx, "", "", v.Tender.Sailing.ID, 500); err == nil {
		bs = full
	}
	d.Missing, _ = s.app.MissingDocs(ctx, bs)
	title := fmt.Sprintf("%s · %s · %s", v.Tender.Ref, v.Tender.Sailing.Lane.Short(), v.Tender.Sailing.Label())
	s.render(w, r, "ops/tender", s.page(r, title, d))
}

func tenderURL(id int64) string { return fmt.Sprintf("/ops/tenders/%d", id) }

func (s *Server) tenderAward(w http.ResponseWriter, r *http.Request) {
	id := idParam(r, "id")
	bid := formInt(r, "bid")
	if err := s.app.Award(r.Context(), userOf(r), id, bid, r.FormValue("reason")); err != nil {
		s.fail(w, r, err, fmt.Sprintf("%s?bid=%d", tenderURL(id), bid))
		return
	}
	s.done(w, r, "Awarded. The winner, the other bidders and every shipper have been told.", tenderURL(id))
}

func (s *Server) tenderExtend(w http.ResponseWriter, r *http.Request) {
	id := idParam(r, "id")
	if err := s.app.ExtendTender(r.Context(), userOf(r), id, formTime(r, "closes")); err != nil {
		s.fail(w, r, err, tenderURL(id))
		return
	}
	s.done(w, r, "Deadline updated and bidders notified.", tenderURL(id))
}

func (s *Server) tenderInvite(w http.ResponseWriter, r *http.Request) {
	id := idParam(r, "id")
	_ = r.ParseForm()
	n, err := s.app.InviteForwarders(r.Context(), userOf(r), id, formInts(r, "company"))
	if err != nil {
		s.fail(w, r, err, tenderURL(id))
		return
	}
	s.done(w, r, fmt.Sprintf("Invited %d forwarder(s).", n), tenderURL(id))
}

func (s *Server) tenderUpload(w http.ResponseWriter, r *http.Request) {
	id := idParam(r, "id")
	if err := s.uploadFromForm(r, "tender", id); err != nil {
		s.fail(w, r, err, tenderURL(id))
		return
	}
	s.done(w, r, "Document uploaded.", tenderURL(id))
}

// ---------- Bookings (staff) ----------

func (s *Server) opsBookings(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	bs, err := s.app.SearchBookings(r.Context(), q.Get("q"), q.Get("status"), 0, 300)
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	missing, _ := s.app.MissingDocs(r.Context(), bs)
	s.render(w, r, "ops/bookings", s.page(r, "Bookings", map[string]any{"Bookings": bs, "Q": q.Get("q"), "Status": q.Get("status"), "Missing": missing}))
}

func (s *Server) opsBooking(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userOf(r)
	b, err := s.app.BookingFor(ctx, u, idParam(r, "id"))
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	d := map[string]any{"B": b, "Steps": s.app.Progress(ctx, b)}
	d["Docs"], _ = s.app.BookingDocReqs(ctx, b)
	d["Invoice"], _ = s.app.InvoiceForBooking(ctx, u, b.ID)
	d["Audit"], _ = s.app.AuditList(ctx, "booking", b.ID, "", 50)
	d["Move"] = s.app.PendingMoveFor(ctx, b.ID)
	d["Company"], _ = s.app.CompanyByID(ctx, b.CompanyID)
	d["Contacts"], _ = s.app.UsersWhere(ctx, "u.company_id=$1 AND u.active", b.CompanyID)
	if b.ContainerID != 0 {
		d["TenderID"] = s.app.TenderForContainer(ctx, b.ContainerID)
	}
	s.render(w, r, "ops/booking", s.page(r, b.Ref, d))
}

func (s *Server) opsBookingUpload(w http.ResponseWriter, r *http.Request) {
	id := idParam(r, "id")
	if err := s.uploadFromForm(r, "booking", id); err != nil {
		s.fail(w, r, err, fmt.Sprintf("/ops/bookings/%d", id))
		return
	}
	s.done(w, r, "Document uploaded on the shipper's behalf.", fmt.Sprintf("/ops/bookings/%d", id))
}

func (s *Server) opsBookingCancel(w http.ResponseWriter, r *http.Request) {
	id := idParam(r, "id")
	if err := s.app.CancelBooking(r.Context(), userOf(r), id, r.FormValue("reason")); err != nil {
		s.fail(w, r, err, fmt.Sprintf("/ops/bookings/%d", id))
		return
	}
	s.done(w, r, "Booking cancelled.", fmt.Sprintf("/ops/bookings/%d", id))
}

// ---------- Invoices (staff) ----------

func (s *Server) opsInvoices(w http.ResponseWriter, r *http.Request) {
	st := r.URL.Query().Get("status")
	vs, err := s.app.Invoices(r.Context(), 0, st)
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	var due, paid int64
	for _, v := range vs {
		if v.Status == "paid" {
			paid += v.TotalCents
		} else if v.Status == "issued" {
			due += v.TotalCents
		}
	}
	s.render(w, r, "ops/invoices", s.page(r, "Invoices", map[string]any{"Invoices": vs, "Status": st, "Due": due, "Paid": paid}))
}

func (s *Server) opsInvoicePDF(w http.ResponseWriter, r *http.Request) {
	inv, err := s.app.InvoiceByID(r.Context(), idParam(r, "id"))
	if err != nil {
		s.notFound(w, r)
		return
	}
	pdf, err := s.app.InvoicePDF(r.Context(), userOf(r), inv)
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	serveFile(w, inv.Number+".pdf", "application/pdf", pdf, false)
}

func (s *Server) opsInvoicePaid(w http.ResponseWriter, r *http.Request) {
	paid := r.FormValue("paid") == "1"
	if err := s.app.MarkInvoicePaid(r.Context(), userOf(r), idParam(r, "id"), paid); err != nil {
		s.fail(w, r, err, "")
		return
	}
	s.done(w, r, "Invoice updated.", "")
}

func (s *Server) tenderClose(w http.ResponseWriter, r *http.Request) {
	id := idParam(r, "id")
	if err := s.app.CloseTenderNow(r.Context(), userOf(r), id); err != nil {
		s.fail(w, r, err, tenderURL(id))
		return
	}
	s.done(w, r, "Bidding closed. Review the bids and award the box.", tenderURL(id))
}
