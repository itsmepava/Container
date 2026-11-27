package web

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/itsmepava/container/internal/app"
	"github.com/itsmepava/container/internal/domain"
	"github.com/itsmepava/container/internal/files"
)

func (s *Server) factoryRoutes(r chi.Router) {
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, homeFor(userOf(r)), http.StatusSeeOther)
	})
	r.Get("/book", s.bookPage)
	r.Post("/book/quote", s.bookQuote)
	r.Post("/book", s.bookPost)
	r.Get("/bookings", s.bookingsPage)
	r.Get("/bookings/{id}", s.bookingPage)
	r.Post("/bookings/{id}/confirm", s.bookingConfirm)
	r.Post("/bookings/{id}/cancel", s.bookingCancel)
	r.Post("/bookings/{id}/docs", s.bookingUpload)
	r.Post("/docs/{id}/delete", s.docDelete)
	r.Post("/moves/{id}/{answer}", s.moveAnswer)
	r.Get("/bookings/{id}/invoice.pdf", s.invoicePDF)
	r.Get("/invoices", s.factoryInvoices)
	r.Get("/team", s.teamPage)
	r.Post("/team/invite", s.teamInvite)
	r.Post("/team/{id}/active", s.teamActive)
}

// ---------- Booking form ----------

type bookForm struct {
	Origins       []app.Port
	Dests         []app.Port
	Origin, Dest  string
	Lane          *app.Lane
	Sailings      []*app.Sailing
	SailingID     int64
	Sailing       *app.Sailing
	Boxes         []domain.ContainerSpec
	BoxCode       string
	Category      domain.Category
	Lines         []domain.CargoLine
	Quote         *app.Quote
	AllowOverflow bool
	Err           string
	Approved      bool
	Touched       bool // the user has entered cargo
	HoldMinutes   int
}

func (f *bookForm) LineRows() []domain.CargoLine {
	if len(f.Lines) == 0 {
		return []domain.CargoLine{{}}
	}
	return f.Lines
}

// buildBookForm reads lane, sailing, container and cargo from the request and prices it.
func (s *Server) buildBookForm(r *http.Request, prefill *app.Booking) (*bookForm, error) {
	ctx := r.Context()
	u := userOf(r)
	f := &bookForm{Approved: u.Approved(), HoldMinutes: s.app.Settings(ctx).HoldMinutes}
	lanes, err := s.app.Lanes(ctx, true)
	if err != nil {
		return nil, err
	}
	seenO, seenD := map[string]bool{}, map[string]bool{}
	for _, l := range lanes {
		if !seenO[l.Origin.Code] {
			seenO[l.Origin.Code] = true
			f.Origins = append(f.Origins, l.Origin)
		}
		if !seenD[l.Dest.Code] {
			seenD[l.Dest.Code] = true
			f.Dests = append(f.Dests, l.Dest)
		}
	}
	get := func(k string) string {
		if v := r.FormValue(k); v != "" {
			return v
		}
		return r.URL.Query().Get(k)
	}
	f.Origin, f.Dest = get("from"), get("to")
	f.SailingID = parseInt64(get("sailing"))
	f.BoxCode = get("box")
	f.Category = domain.Category(get("category"))
	f.AllowOverflow = r.FormValue("overflow") == "on"
	if prefill != nil && prefill.Sailing != nil {
		f.Origin, f.Dest = prefill.Sailing.Lane.Origin.Code, prefill.Sailing.Lane.Dest.Code
		f.BoxCode, f.Category = prefill.BoxCode, prefill.Category
		for _, l := range prefill.Lines {
			f.Lines = append(f.Lines, l.CargoLine)
		}
	}
	if f.Origin == "" {
		// Default to the lane this factory booked last.
		var o, d string
		if s.app.DB.QueryRow(ctx, `SELECT l.origin, l.dest FROM bookings b JOIN sailings s ON s.id=b.sailing_id JOIN lanes l ON l.id=s.lane_id
			WHERE b.company_id=$1 AND l.active ORDER BY b.created_at DESC LIMIT 1`, u.CompanyID).Scan(&o, &d) == nil {
			f.Origin, f.Dest = o, d
		}
	}
	if f.Origin == "" && len(lanes) > 0 {
		f.Origin = lanes[0].Origin.Code
	}
	if f.Dest == "" && len(f.Dests) > 0 {
		f.Dest = f.Dests[0].Code
		for _, l := range lanes {
			if l.Origin.Code == f.Origin {
				f.Dest = l.Dest.Code
				break
			}
		}
	}
	for _, l := range lanes {
		if l.Origin.Code == f.Origin && l.Dest.Code == f.Dest {
			f.Lane = l
		}
	}
	if f.Category == "" || !f.Category.Valid() {
		f.Category = domain.CatFabric
	}
	if r.Method == http.MethodPost {
		f.Lines = parseLines(r)
		f.Touched = len(f.Lines) > 0
	} else if len(f.Lines) > 0 {
		f.Touched = true
	}
	if f.Lane == nil {
		return f, nil
	}
	if f.Sailings, err = s.app.BookableSailings(ctx, f.Lane.ID); err != nil {
		return nil, err
	}
	for _, x := range f.Sailings {
		if x.ID == f.SailingID {
			f.Sailing = x
		}
	}
	if f.Sailing == nil && len(f.Sailings) > 0 {
		// Default to the first sailing that still has space in its pooled box.
		f.Sailing = f.Sailings[0]
		for _, x := range f.Sailings {
			code := f.BoxCode
			if boxes, _ := s.app.BoxCodesOnSailing(ctx, x.ID); len(boxes) > 0 && code == "" {
				code = boxes[0].Code
			}
			if q, err := s.app.Quote(ctx, app.QuoteInput{SailingID: x.ID, BoxCode: code, Category: f.Category}); err == nil && q.Space.Free() >= 1 {
				f.Sailing = x
				break
			}
		}
	}
	if f.Sailing == nil {
		return f, nil
	}
	f.SailingID = f.Sailing.ID
	if f.Boxes, err = s.app.BoxCodesOnSailing(ctx, f.Sailing.ID); err != nil {
		return nil, err
	}
	ok := false
	for _, b := range f.Boxes {
		ok = ok || b.Code == f.BoxCode
	}
	if !ok && len(f.Boxes) > 0 {
		f.BoxCode = f.Boxes[0].Code
	}
	f.Quote, err = s.app.Quote(ctx, app.QuoteInput{SailingID: f.Sailing.ID, BoxCode: f.BoxCode, Category: f.Category, Lines: f.Lines})
	return f, err
}

func parseInt64(s string) int64 {
	var n int64
	fmt.Sscan(strings.TrimSpace(s), &n)
	return n
}

// parseLines reads the repeated cargo inputs, skipping empty rows.
func parseLines(r *http.Request) []domain.CargoLine {
	_ = r.ParseForm()
	hs := r.Form["hs"]
	var out []domain.CargoLine
	for i := range hs {
		at := func(k string) string {
			if vs := r.Form[k]; i < len(vs) {
				return vs[i]
			}
			return ""
		}
		l := domain.CargoLine{HSCode: strings.TrimSpace(hs[i]), Pieces: int(parseFloat(at("pieces"))), LengthCm: parseFloat(at("length")),
			WidthCm: parseFloat(at("width")), HeightCm: parseFloat(at("height")), KgEach: parseFloat(at("kg"))}
		if l.HSCode == "" && l.Pieces == 0 && l.LengthCm == 0 && l.WidthCm == 0 && l.HeightCm == 0 && l.KgEach == 0 {
			continue
		}
		out = append(out, l)
	}
	return out
}

func (s *Server) bookPage(w http.ResponseWriter, r *http.Request) {
	var prefill *app.Booking
	if id := parseInt64(r.URL.Query().Get("rebook")); id != 0 {
		prefill, _ = s.app.BookingFor(r.Context(), userOf(r), id)
	}
	f, err := s.buildBookForm(r, prefill)
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	p := s.page(r, "Book LCL space into a pooled FCL", f)
	s.render(w, r, "factory/book", p)
}

func (s *Server) bookQuote(w http.ResponseWriter, r *http.Request) {
	f, err := s.buildBookForm(r, nil)
	if err != nil {
		s.app.Log.Error("quote", "err", err)
		http.Error(w, "quote failed", 500)
		return
	}
	s.fragment(w, "factory/book", "quote-swap", s.page(r, "", f))
}

func (s *Server) bookPost(w http.ResponseWriter, r *http.Request) {
	f, err := s.buildBookForm(r, nil)
	if err != nil {
		s.fail(w, r, err, "/f/book")
		return
	}
	if f.Sailing == nil {
		s.fail(w, r, app.Errf("Choose a sailing week."), "/f/book")
		return
	}
	b, err := s.app.HoldBooking(r.Context(), userOf(r), app.BookingInput{
		QuoteInput:    app.QuoteInput{SailingID: f.Sailing.ID, BoxCode: f.BoxCode, Category: f.Category, Lines: f.Lines},
		AllowOverflow: f.AllowOverflow,
	})
	if err != nil {
		msg, ok := app.IsUserError(err)
		if !ok {
			s.app.Log.Error("hold", "err", err)
			msg = "Something went wrong. Please try again."
		}
		f.Err = msg
		p := s.page(r, "Book LCL space into a pooled FCL", f)
		p.Flash = &Flash{Kind: "bad", Msg: msg}
		w.WriteHeader(http.StatusUnprocessableEntity)
		s.render(w, r, "factory/book", p)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/f/bookings/%d", b.ID), http.StatusSeeOther)
}

// ---------- Bookings ----------

type bookingRow struct {
	B       *app.Booking
	Steps   []app.Step
	Missing []string
}

type bookingsData struct {
	Rows     []bookingRow
	Tab      string
	Approved bool
	Status   string
	Note     string
}

func (s *Server) bookingsPage(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	tab := r.URL.Query().Get("tab")
	bs, err := s.app.CompanyBookings(r.Context(), u.CompanyID, true)
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	missing, _ := s.app.MissingDocs(r.Context(), bs)
	d := bookingsData{Tab: tab, Approved: u.Approved(), Status: u.CompanyStatus}
	if c, err := s.app.CompanyByID(r.Context(), u.CompanyID); err == nil {
		d.Note = c.StatusNote
	}
	for _, b := range bs {
		active := b.Status == "booked" || (b.Status == "held" && b.Active(s.app.Now()))
		var invoiced bool
		if b.Priced() {
			_, err := s.app.InvoiceForBooking(r.Context(), u, b.ID)
			invoiced = err == nil
		}
		past := !active || invoiced && b.Sailing.Vessel() != nil && b.Sailing.Vessel().ETA.Before(s.app.Now())
		if (tab == "past") != past {
			continue
		}
		d.Rows = append(d.Rows, bookingRow{B: b, Steps: s.app.Progress(r.Context(), b), Missing: missing[b.ID]})
	}
	s.render(w, r, "factory/bookings", s.page(r, "My bookings", d))
}

type bookingData struct {
	B          *app.Booking
	Steps      []app.Step
	Docs       []app.DocReq
	Invoice    *app.Invoice
	Move       *app.MoveRequest
	Vessel     *app.VesselOption
	Carrier    string
	Box        *boxView
	CanCancel  bool
	CanUpload  bool
	HoldActive bool
	PerWM      float64
	Lines      []app.BookingLine
	ToSailing  *app.Sailing
}

// boxView is the shipper's anonymous view of its container.
type boxView struct {
	Code    string
	Usable  float64
	Mine    float64
	Others  float64
	Free    float64
	FillPct float64
}

func (s *Server) bookingPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userOf(r)
	b, err := s.app.BookingFor(ctx, u, idParam(r, "id"))
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	d := bookingData{B: b, Steps: s.app.Progress(ctx, b), Vessel: b.Sailing.Vessel(), Lines: b.Lines}
	d.Docs, _ = s.app.BookingDocReqs(ctx, b)
	d.Invoice, _ = s.app.InvoiceForBooking(ctx, u, b.ID)
	d.Move = s.app.PendingMoveFor(ctx, b.ID)
	if d.Move != nil && d.Move.ToSailingID != 0 {
		d.ToSailing, _ = s.app.SailingByID(ctx, d.Move.ToSailingID)
	}
	d.HoldActive = b.Status == "held" && b.Active(s.app.Now())
	d.CanUpload = (b.Status == "booked" || d.HoldActive) && (d.Vessel == nil || s.app.Now().Before(d.Vessel.ETD))
	d.CanCancel = (b.Status == "held" || b.Status == "booked") && b.Sailing.Status == "open" && !b.Priced()
	if b.Priced() && b.WM > 0 {
		d.PerWM = float64(*b.TotalCents) / 100 / b.WM
	}
	if b.Mode == "pool" && b.ContainerID != 0 {
		var winner int64
		if tid := s.app.TenderForContainer(ctx, b.ContainerID); tid != 0 {
			_ = s.app.DB.QueryRow(ctx, `SELECT COALESCE(c.name,''), COALESCE(w.vessel_id,0) FROM tenders t LEFT JOIN bids w ON w.id=t.awarded_bid_id LEFT JOIN companies c ON c.id=w.company_id WHERE t.id=$1`, tid).Scan(&d.Carrier, &winner)
			if winner != 0 {
				d.Vessel = b.Sailing.VesselByID(winner)
			}
		}
		if c, err := s.app.ContainerByID(ctx, b.ContainerID); err == nil {
			st := s.app.Settings(ctx)
			bv := &boxView{Code: c.BoxCode, Usable: c.Spec.UsableCBM(st.Stowage), Mine: b.CBM}
			var used float64
			_ = s.app.DB.QueryRow(ctx, `SELECT COALESCE(sum(cbm),0) FROM bookings WHERE container_id=$1 AND status='booked'`, c.ID).Scan(&used)
			bv.Others = domain.Round(used-b.CBM, 2)
			bv.Free = domain.Round(bv.Usable-used, 2)
			if bv.Free < 0 {
				bv.Free = 0
			}
			bv.FillPct = used / bv.Usable * 100
			d.Box = bv
		}
	}
	title := b.Ref
	s.render(w, r, "factory/booking", s.page(r, title, d))
}

func (s *Server) bookingConfirm(w http.ResponseWriter, r *http.Request) {
	b, err := s.app.ConfirmBooking(r.Context(), userOf(r), idParam(r, "id"))
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	msg := fmt.Sprintf("Booked %s CBM. Upload your commercial invoice before the CFS cut-off.", strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", b.CBM), "0"), "."))
	if b.ContainerID == 0 {
		msg = "Booked. Your cargo doesn't fit this week's container yet, so Eimsky ops will offer the next sailing or co-loading. Your cost cap stays the same."
	}
	s.done(w, r, msg, fmt.Sprintf("/f/bookings/%d", b.ID))
}

func (s *Server) bookingCancel(w http.ResponseWriter, r *http.Request) {
	id := idParam(r, "id")
	if err := s.app.CancelBooking(r.Context(), userOf(r), id, r.FormValue("reason")); err != nil {
		s.fail(w, r, err, "")
		return
	}
	s.done(w, r, "Booking cancelled and the space released.", "/f/bookings")
}

func (s *Server) bookingUpload(w http.ResponseWriter, r *http.Request) {
	id := idParam(r, "id")
	back := fmt.Sprintf("%s#documents", backURL(r, fmt.Sprintf("/f/bookings/%d", id)))
	if err := s.uploadFromForm(r, "booking", id); err != nil {
		s.fail(w, r, err, back)
		return
	}
	s.done(w, r, "Document uploaded and encrypted.", back)
}

// uploadFromForm reads the "file" field and stores it for an owner.
func (s *Server) uploadFromForm(r *http.Request, ownerKind string, ownerID int64) error {
	if err := r.ParseMultipartForm(12 << 20); err != nil {
		return app.Errf("That file is too large. The limit is 10 MB.")
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		return app.Errf("Choose a file to upload.")
	}
	defer file.Close()
	data, ct, err := files.Sniff(file)
	if err != nil {
		if err == files.ErrTooLarge || err == files.ErrType {
			return app.Errf("%s", strings.ToUpper(err.Error()[:1])+err.Error()[1:]+".")
		}
		return app.Errf("We couldn't read that file: %s.", err.Error())
	}
	_, err = s.app.UploadDoc(r.Context(), userOf(r), ownerKind, ownerID, r.FormValue("kind"), hdr.Filename, data, ct)
	return err
}

func (s *Server) docDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.app.DeleteDoc(r.Context(), userOf(r), idParam(r, "id")); err != nil {
		s.fail(w, r, err, "")
		return
	}
	s.done(w, r, "Document removed.", "")
}

func (s *Server) moveAnswer(w http.ResponseWriter, r *http.Request) {
	accept := chi.URLParam(r, "answer") == "accept"
	if err := s.app.RespondMove(r.Context(), userOf(r), idParam(r, "id"), accept); err != nil {
		s.fail(w, r, err, "")
		return
	}
	msg := "Thanks. Your booking has moved and your cost cap is unchanged."
	if !accept {
		msg = "Understood. Your cargo stays on this sailing with our co-load partner, within your cost cap."
	}
	s.done(w, r, msg, "")
}

func (s *Server) invoicePDF(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	inv, err := s.app.InvoiceForBooking(r.Context(), u, idParam(r, "id"))
	if err != nil {
		s.notFound(w, r)
		return
	}
	pdf, err := s.app.InvoicePDF(r.Context(), u, inv)
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	serveFile(w, inv.Number+".pdf", "application/pdf", pdf, r.URL.Query().Get("download") == "1")
}

func (s *Server) factoryInvoices(w http.ResponseWriter, r *http.Request) {
	vs, err := s.app.Invoices(r.Context(), userOf(r).CompanyID, "")
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
	s.render(w, r, "factory/invoices", s.page(r, "Invoices", map[string]any{"Invoices": vs, "Due": due, "Paid": paid}))
}

// ---------- Team ----------

func (s *Server) teamPage(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	us, err := s.app.UsersWhere(r.Context(), "u.company_id=$1", u.CompanyID)
	if err != nil {
		s.fail(w, r, err, "")
		return
	}
	s.render(w, r, "factory/team", s.page(r, "Team", us))
}

func (s *Server) teamInvite(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	if u.Role != app.RoleFactoryAdmin {
		s.notFound(w, r)
		return
	}
	role := app.RoleFactoryStaff
	if r.FormValue("role") == "factory_admin" {
		role = app.RoleFactoryAdmin
	}
	if _, _, err := s.app.InviteUser(r.Context(), u, u.CompanyID, role, r.FormValue("name"), r.FormValue("email")); err != nil {
		s.fail(w, r, err, "/f/team")
		return
	}
	s.done(w, r, "Invitation sent. The link works for 7 days.", "/f/team")
}

func (s *Server) teamActive(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	target, err := s.app.UserByID(r.Context(), idParam(r, "id"))
	if err != nil || u.Role != app.RoleFactoryAdmin || target.CompanyID != u.CompanyID {
		s.notFound(w, r)
		return
	}
	on := r.FormValue("active") == "1"
	if err := s.app.SetUserActive(r.Context(), u, target.ID, on); err != nil {
		s.fail(w, r, err, "/f/team")
		return
	}
	s.done(w, r, "Saved.", "/f/team")
}
