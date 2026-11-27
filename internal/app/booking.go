package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/itsmepava/container/internal/domain"
	"github.com/jackc/pgx/v5"
)

type BookingLine struct {
	LineNo int
	domain.CargoLine
	Description string
}

type Booking struct {
	ID            int64
	Ref           string
	CompanyID     int64
	CompanyName   string
	CreatedBy     int64
	SailingID     int64
	BoxCode       string
	Category      domain.Category
	Pieces        int
	CBM           float64
	Kg            float64
	WM            float64
	CapCents      int64
	Status        string
	HoldExpiresAt *time.Time
	ContainerID   int64
	Mode          string
	FreightCents  *int64
	FeeCents      *int64
	TotalCents    *int64
	CapApplied    bool
	PricedAt      *time.Time
	Note          string
	ConfirmedAt   *time.Time
	CancelledAt   *time.Time
	CreatedAt     time.Time

	Lines   []BookingLine
	Sailing *Sailing
}

func (b *Booking) Active(now time.Time) bool {
	return b.Status == "booked" || (b.Status == "held" && b.HoldExpiresAt != nil && now.Before(*b.HoldExpiresAt))
}

func (b *Booking) Priced() bool { return b.PricedAt != nil && b.TotalCents != nil }

func (b *Booking) StatusLabel() string {
	switch b.Status {
	case "held":
		return "Held"
	case "booked":
		if b.Priced() {
			return "Priced"
		}
		if b.Mode == "coload" {
			return "Co-load"
		}
		if b.ContainerID == 0 {
			return "Waiting to place"
		}
		return "Booked"
	case "cancelled":
		return "Cancelled"
	case "expired":
		return "Hold expired"
	}
	return b.Status
}

const bookingCols = `b.id,b.ref,b.company_id,c.name,b.created_by,b.sailing_id,b.box_code,b.category,b.pieces,b.cbm,b.kg,b.wm,b.cap_cents,b.status,
	b.hold_expires_at,COALESCE(b.container_id,0),b.mode,b.freight_cents,b.fee_cents,b.total_cents,b.cap_applied,b.priced_at,b.note,b.confirmed_at,b.cancelled_at,b.created_at`

func scanBooking(row pgx.Row) (*Booking, error) {
	b := &Booking{}
	var cat string
	err := row.Scan(&b.ID, &b.Ref, &b.CompanyID, &b.CompanyName, &b.CreatedBy, &b.SailingID, &b.BoxCode, &cat, &b.Pieces, &b.CBM, &b.Kg, &b.WM, &b.CapCents, &b.Status,
		&b.HoldExpiresAt, &b.ContainerID, &b.Mode, &b.FreightCents, &b.FeeCents, &b.TotalCents, &b.CapApplied, &b.PricedAt, &b.Note, &b.ConfirmedAt, &b.CancelledAt, &b.CreatedAt)
	b.Category = domain.Category(cat)
	return b, err
}

func (a *App) queryBookings(ctx context.Context, q Querier, where string, args ...any) ([]*Booking, error) {
	rows, err := q.Query(ctx, `SELECT `+bookingCols+` FROM bookings b JOIN companies c ON c.id=b.company_id WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Booking
	for rows.Next() {
		b, err := scanBooking(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (a *App) loadLines(ctx context.Context, bs []*Booking) error {
	if len(bs) == 0 {
		return nil
	}
	byID := map[int64]*Booking{}
	var ids []int64
	for _, b := range bs {
		byID[b.ID] = b
		ids = append(ids, b.ID)
	}
	rows, err := a.DB.Query(ctx, `SELECT booking_id,line_no,hs_code,pieces,length_cm,width_cm,height_cm,kg_each FROM booking_lines WHERE booking_id = ANY($1) ORDER BY booking_id, line_no`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var bid int64
		var l BookingLine
		if err := rows.Scan(&bid, &l.LineNo, &l.HSCode, &l.Pieces, &l.LengthCm, &l.WidthCm, &l.HeightCm, &l.KgEach); err != nil {
			return err
		}
		byID[bid].Lines = append(byID[bid].Lines, l)
	}
	return rows.Err()
}

// BookingFor loads a booking the user may see (own company, or staff).
func (a *App) BookingFor(ctx context.Context, u *User, id int64) (*Booking, error) {
	bs, err := a.queryBookings(ctx, a.DB, `b.id=$1`, id)
	if err != nil {
		return nil, err
	}
	if len(bs) == 0 {
		return nil, ErrNotFound
	}
	b := bs[0]
	if !u.IsStaff() && b.CompanyID != u.CompanyID {
		return nil, ErrNotFound
	}
	if err := a.loadLines(ctx, bs); err != nil {
		return nil, err
	}
	descs := map[string]bool{}
	for _, l := range b.Lines {
		descs[l.HSCode] = true
	}
	var codes []string
	for c := range descs {
		codes = append(codes, c)
	}
	d := a.HSDescriptions(ctx, codes)
	for i := range b.Lines {
		b.Lines[i].Description = d[b.Lines[i].HSCode]
	}
	b.Sailing, err = a.SailingByID(ctx, b.SailingID)
	return b, err
}

// CompanyBookings lists a factory's bookings, newest first.
func (a *App) CompanyBookings(ctx context.Context, companyID int64, includeClosed bool) ([]*Booking, error) {
	where := `b.company_id=$1 AND b.status IN ('held','booked')`
	if includeClosed {
		where = `b.company_id=$1`
	}
	bs, err := a.queryBookings(ctx, a.DB, where+` ORDER BY b.created_at DESC`, companyID)
	if err != nil {
		return nil, err
	}
	cache := map[int64]*Sailing{}
	for _, b := range bs {
		if s, ok := cache[b.SailingID]; ok {
			b.Sailing = s
			continue
		}
		if b.Sailing, err = a.SailingByID(ctx, b.SailingID); err != nil {
			return nil, err
		}
		cache[b.SailingID] = b.Sailing
	}
	return bs, a.loadLines(ctx, bs)
}

// ---------- Pool capacity ----------

// PoolSpace is what a shipper sees about a sailing's pooled box.
type PoolSpace struct {
	BoxCode    string
	Containers []*Container
	Usable     float64
	Used       float64
	Categories map[domain.Category]bool
	Grades     map[domain.Grade]bool
}

// Free is the space left, never below zero (a pool can be overbooked while ops plans).
func (p PoolSpace) Free() float64 {
	f := domain.Round(p.Usable-p.Used, 2)
	if f < 0 {
		return 0
	}
	return f
}
func (p PoolSpace) FillPct() float64 {
	if p.Usable <= 0 {
		return 0
	}
	return p.Used / p.Usable * 100
}

func (a *App) poolSpace(ctx context.Context, q Querier, sailingID int64, boxCode string, excludeBooking int64) (PoolSpace, error) {
	st := a.Settings(ctx)
	ps := PoolSpace{BoxCode: boxCode, Categories: map[domain.Category]bool{}, Grades: map[domain.Grade]bool{}}
	cs, err := a.Containers(ctx, q, sailingID)
	if err != nil {
		return ps, err
	}
	for _, c := range cs {
		if c.BoxCode == boxCode && c.Status == "planning" {
			ps.Containers = append(ps.Containers, c)
			ps.Usable += c.Spec.UsableCBM(st.Stowage)
			ps.Grades[c.Grade] = true
		}
	}
	rows, err := q.Query(ctx, `SELECT cbm, category FROM bookings WHERE sailing_id=$1 AND box_code=$2 AND mode='pool' AND id<>$3
		AND (status='booked' OR (status='held' AND hold_expires_at > $4))`, sailingID, boxCode, excludeBooking, a.Now())
	if err != nil {
		return ps, err
	}
	defer rows.Close()
	for rows.Next() {
		var cbm float64
		var cat string
		if err := rows.Scan(&cbm, &cat); err != nil {
			return ps, err
		}
		ps.Used += cbm
		ps.Categories[domain.Category(cat)] = true
	}
	ps.Usable = domain.Round(ps.Usable, 2)
	ps.Used = domain.Round(ps.Used, 2)
	return ps, rows.Err()
}

// BoxCodesOnSailing lists the container types shippers can book on a sailing.
func (a *App) BoxCodesOnSailing(ctx context.Context, sailingID int64) ([]domain.ContainerSpec, error) {
	cs, err := a.Containers(ctx, a.DB, sailingID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []domain.ContainerSpec
	for _, c := range cs {
		if c.Status == "planning" && !seen[c.BoxCode] {
			seen[c.BoxCode] = true
			out = append(out, c.Spec)
		}
	}
	return out, nil
}

// ---------- Quotes ----------

type QuoteInput struct {
	SailingID int64
	BoxCode   string
	Category  domain.Category
	Lines     []domain.CargoLine
}

type Check struct {
	OK  bool
	Msg string
}

type Quote struct {
	Sailing   *Sailing
	Vessel    *VesselOption
	Space     PoolSpace
	Lines     []domain.CargoLine
	LineErrs  []string
	Pieces    int
	CBM       float64
	Kg        float64
	WM        float64
	CapCents  int64
	Fits      bool
	Problems  []string // blocking problems
	Warnings  []string
	CanBook   bool
	FreeAfter float64
}

func (q *Quote) AllPassed() bool { return len(q.Problems) == 0 && len(q.Warnings) == 0 && q.CBM > 0 }

// Quote prices and checks a booking without saving it.
func (a *App) Quote(ctx context.Context, in QuoteInput) (*Quote, error) {
	return a.quote(ctx, a.DB, in, 0)
}

func (a *App) quote(ctx context.Context, q Querier, in QuoteInput, excludeBooking int64) (*Quote, error) {
	s, err := a.SailingByID(ctx, in.SailingID)
	if err != nil {
		return nil, err
	}
	qt := &Quote{Sailing: s, Vessel: s.Vessel()}
	qt.Space, err = a.poolSpace(ctx, q, s.ID, in.BoxCode, excludeBooking)
	if err != nil {
		return nil, err
	}
	hsOK := true
	for i, l := range in.Lines {
		l.HSCode = domain.NormalizeHSCode(l.HSCode)
		if err := l.Validate(); err != nil {
			qt.LineErrs = append(qt.LineErrs, fmt.Sprintf("Line %d: %s", i+1, err.Error()))
			if domain.ValidateHSCode(l.HSCode) != nil {
				hsOK = false
			}
			continue
		}
		qt.Lines = append(qt.Lines, l)
	}
	qt.Pieces, qt.CBM, qt.Kg = domain.Totals(qt.Lines)
	qt.WM = domain.WM(qt.CBM, qt.Kg)
	qt.CapCents = domain.Cents(s.Lane.EstimatePerWM * qt.WM)
	qt.FreeAfter = domain.Round(qt.Space.Free()-qt.CBM, 2)
	qt.Fits = qt.CBM <= qt.Space.Free()+1e-9

	if len(in.Lines) == 0 {
		qt.Problems = append(qt.Problems, "Add at least one cargo line.")
	}
	qt.Problems = append(qt.Problems, qt.LineErrs...)
	if !s.Bookable(a.Now()) {
		qt.Problems = append(qt.Problems, "This sailing is closed for booking. Choose a later week.")
	}
	if !in.Category.Valid() {
		qt.Problems = append(qt.Problems, "Choose a cargo category.")
	} else if in.Category == domain.CatDG {
		qt.Problems = append(qt.Problems, "Dangerous goods aren't pooled in phase 1.")
	}
	if len(qt.Space.Containers) == 0 {
		qt.Problems = append(qt.Problems, "There's no "+in.BoxCode+" on this sailing. Choose another container type.")
	} else if in.Category.Valid() && in.Category != domain.CatDG {
		gradeOK := false
		for g := range qt.Space.Grades {
			if domain.GradeAllows(g, in.Category) == nil {
				gradeOK = true
			}
		}
		if !gradeOK {
			qt.Problems = append(qt.Problems, in.Category.Label()+" isn't allowed in this container's grade. Choose another week or container.")
		}
		for c := range qt.Space.Categories {
			if !domain.Compatible(c, in.Category) {
				qt.Problems = append(qt.Problems, in.Category.Label()+" can't share this container with cargo already booked. Choose another week.")
				break
			}
		}
	}
	if qt.CBM > 0 && !qt.Fits {
		qt.Warnings = append(qt.Warnings, fmt.Sprintf("Your %.2f CBM doesn't fit the %.1f CBM left. You can still book: Eimsky will offer the next sailing or co-loading, and your cost cap stays the same.", qt.CBM, qt.Space.Free()))
	}
	if qt.Kg > 0 && len(qt.Space.Containers) > 0 && qt.Kg > qt.Space.Containers[0].Spec.PayloadKg {
		qt.Problems = append(qt.Problems, "This cargo is heavier than the container's payload.")
	}
	_ = hsOK
	qt.CanBook = len(qt.Problems) == 0 && qt.CBM > 0
	return qt, nil
}

// QuoteChecks lists the checks shown under the booking form.
func (q *Quote) Checks() []Check {
	var out []Check
	hsOK := len(q.LineErrs) == 0 && len(q.Lines) > 0
	out = append(out, Check{hsOK, "HS codes and sizes valid"})
	catOK := true
	for _, p := range q.Problems {
		if strings.Contains(p, "category") || strings.Contains(p, "grade") || strings.Contains(p, "share") || strings.Contains(p, "Dangerous") {
			catOK = false
		}
	}
	out = append(out, Check{catOK, "Category allowed in this container"})
	out = append(out, Check{q.Fits && q.CBM > 0, "Fits remaining space"})
	out = append(out, Check{q.Sailing.Bookable(time.Now()), "Before CFS cut-off"})
	return out
}

// ---------- Holds and confirmation ----------

type BookingInput struct {
	QuoteInput
	AllowOverflow bool
}

// HoldBooking checks a quote and holds the space for the configured minutes.
func (a *App) HoldBooking(ctx context.Context, u *User, in BookingInput) (*Booking, error) {
	if !u.IsFactory() || !u.Approved() {
		return nil, Errf("Your factory account is waiting for Eimsky's approval. You can book once it's approved.")
	}
	st := a.Settings(ctx)
	var id int64
	err := a.Tx(ctx, func(tx pgx.Tx) error {
		// Serialise bookings on the same sailing so space can't be oversold.
		if _, err := tx.Exec(ctx, `SELECT id FROM sailings WHERE id=$1 FOR UPDATE`, in.SailingID); err != nil {
			return err
		}
		qt, err := a.quote(ctx, tx, in.QuoteInput, 0)
		if err != nil {
			return err
		}
		if !qt.CanBook {
			return Errf("%s", strings.Join(qt.Problems, " "))
		}
		if !qt.Fits && !in.AllowOverflow {
			return Errf("Your cargo doesn't fit the %.1f CBM left. Tick “Book anyway” or choose another week.", qt.Space.Free())
		}
		var seq int64
		if err := tx.QueryRow(ctx, `SELECT nextval('booking_ref_seq')`).Scan(&seq); err != nil {
			return err
		}
		ref := fmt.Sprintf("EIM-%02d-%04d", qt.Sailing.Week, seq)
		exp := a.Now().Add(time.Duration(st.HoldMinutes) * time.Minute)
		if err := tx.QueryRow(ctx, `INSERT INTO bookings(ref,company_id,created_by,sailing_id,box_code,category,pieces,cbm,kg,wm,cap_cents,status,hold_expires_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'held',$12) RETURNING id`,
			ref, u.CompanyID, u.ID, in.SailingID, in.BoxCode, in.Category, qt.Pieces, qt.CBM, qt.Kg, qt.WM, qt.CapCents, exp).Scan(&id); err != nil {
			return err
		}
		for i, l := range qt.Lines {
			if _, err := tx.Exec(ctx, `INSERT INTO booking_lines(booking_id,line_no,hs_code,pieces,length_cm,width_cm,height_cm,kg_each,cbm,kg) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
				id, i+1, l.HSCode, l.Pieces, l.LengthCm, l.WidthCm, l.HeightCm, l.KgEach, l.CBM(), l.Kg()); err != nil {
				return err
			}
		}
		return a.Audit(ctx, tx, u, "booking.hold", "booking", id, fmt.Sprintf("Held %.2f CBM on %s %s", qt.CBM, qt.Sailing.Lane.Short(), qt.Sailing.Label()), nil)
	})
	if err != nil {
		return nil, err
	}
	return a.BookingFor(ctx, u, id)
}

// ConfirmBooking turns a hold into a booking and places it in a container if it fits.
func (a *App) ConfirmBooking(ctx context.Context, u *User, id int64) (*Booking, error) {
	b, err := a.BookingFor(ctx, u, id)
	if err != nil {
		return nil, err
	}
	if b.Status == "booked" {
		return b, nil
	}
	if b.Status != "held" || b.HoldExpiresAt == nil || a.Now().After(*b.HoldExpiresAt) {
		return nil, Errf("The 15-minute hold has run out. Book again to check the space left.")
	}
	if !b.Sailing.Bookable(a.Now()) {
		return nil, Errf("This sailing closed for booking while you were confirming. Book a later week.")
	}
	err = a.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT id FROM sailings WHERE id=$1 FOR UPDATE`, b.SailingID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE bookings SET status='booked', confirmed_at=now(), hold_expires_at=NULL WHERE id=$1 AND status='held'`, id); err != nil {
			return err
		}
		if err := a.tryPlace(ctx, tx, b); err != nil {
			return err
		}
		if err := a.Audit(ctx, tx, u, "booking.confirm", "booking", id, fmt.Sprintf("Booked %s · %.2f CBM", b.Ref, b.CBM), nil); err != nil {
			return err
		}
		vessel := b.Sailing.Vessel()
		body := fmt.Sprintf("%s: %.2f CBM on %s, %s. Estimated cost up to USD %s.\nDocuments are due before the CFS cut-off, %s.",
			b.Ref, b.CBM, b.Sailing.Lane.Short(), b.Sailing.Label(), domain.MoneyShort(b.CapCents), fmtTime(b.Sailing.CFSCutoff))
		if vessel != nil {
			body += "\nVessel: " + vessel.Label()
		}
		return a.NotifyCompany(ctx, tx, b.CompanyID, Note{Title: "Booking confirmed: " + b.Ref, Body: body, Link: fmt.Sprintf("/f/bookings/%d", id), Email: true})
	})
	if err != nil {
		return nil, err
	}
	return a.BookingFor(ctx, u, id)
}

// tryPlace puts a booked lot into the first container of its type that takes it.
func (a *App) tryPlace(ctx context.Context, q Querier, b *Booking) error {
	boxes, byBox, err := a.loadBoxes(ctx, q, b.SailingID)
	if err != nil {
		return err
	}
	lot := domain.Lot{ID: b.ID, CBM: b.CBM, Kg: b.Kg, Category: b.Category}
	for i, box := range boxes {
		if box.Spec.Code != b.BoxCode || box.Locked {
			continue
		}
		if box.CanPlace(lot) == nil {
			_, err := q.Exec(ctx, `UPDATE bookings SET container_id=$2 WHERE id=$1`, b.ID, byBox[i].ID)
			return err
		}
	}
	return nil
}

// CancelBooking releases a held or booked lot before the plan is locked.
func (a *App) CancelBooking(ctx context.Context, u *User, id int64, reason string) error {
	b, err := a.BookingFor(ctx, u, id)
	if err != nil {
		return err
	}
	if b.Status != "held" && b.Status != "booked" {
		return Errf("This booking is already %s.", strings.ToLower(b.StatusLabel()))
	}
	if b.Sailing.Status != "open" || b.Priced() {
		return Errf("The plan for this sailing is locked, so the booking can't be cancelled here. Contact Eimsky ops.")
	}
	return a.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE bookings SET status='cancelled', cancelled_at=now(), container_id=NULL, note=$2 WHERE id=$1`, id, strings.TrimSpace(reason)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE move_requests SET status='cancelled', resolved_at=now() WHERE booking_id=$1 AND status='pending'`, id); err != nil {
			return err
		}
		if err := a.Audit(ctx, tx, u, "booking.cancel", "booking", id, "Cancelled "+b.Ref, map[string]string{"reason": reason}); err != nil {
			return err
		}
		if b.Status == "booked" {
			return a.NotifyStaff(ctx, tx, Note{Title: "Booking cancelled: " + b.Ref, Body: fmt.Sprintf("%s cancelled %.2f CBM on %s %s.", b.CompanyName, b.CBM, b.Sailing.Lane.Short(), b.Sailing.Label()), Link: fmt.Sprintf("/ops/sailings/%d/plan", b.SailingID)})
		}
		return nil
	})
}

// ExpireHolds marks holds past their time as expired.
func (a *App) ExpireHolds(ctx context.Context) (int64, error) {
	tag, err := a.DB.Exec(ctx, `UPDATE bookings SET status='expired' WHERE status='held' AND hold_expires_at < $1`, a.Now())
	return tag.RowsAffected(), err
}

// ---------- Progress tracker ----------

type Step struct {
	Title  string
	Detail string
	Done   bool
	Active bool
	Now    string // what is happening while this step is the active one
}

// Progress returns the five tracker steps for a booking.
func (a *App) Progress(ctx context.Context, b *Booking) []Step {
	var tenderStatus string
	var bidders int
	if b.ContainerID != 0 {
		_ = a.DB.QueryRow(ctx, `SELECT t.status, (SELECT count(*) FROM bids WHERE tender_id=t.id) FROM tenders t WHERE t.container_id=$1`, b.ContainerID).Scan(&tenderStatus, &bidders)
	}
	var invoiced bool
	_ = a.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM invoices WHERE booking_id=$1 AND status<>'void')`, b.ID).Scan(&invoiced)

	booked := b.Status == "booked"
	finalized := booked && (tenderStatus != "" || b.Mode == "coload")
	closed := finalized && (tenderStatus == "closed" || tenderStatus == "awarded" || b.Mode == "coload")
	awarded := b.Priced()
	steps := []Step{
		{Title: "Booked", Detail: fmt.Sprintf("%.1f CBM held", b.CBM), Done: booked},
		{Title: "FCL finalized", Detail: "plan locked before cut-off", Done: finalized},
		{Title: "Bids closed", Detail: "forwarders bid", Done: closed},
		{Title: "Awarded", Detail: "your cost is final", Done: awarded},
		{Title: "Invoiced", Detail: "after documents", Done: invoiced},
	}
	if finalized && b.Mode != "coload" {
		var wm float64
		_ = a.DB.QueryRow(ctx, `SELECT COALESCE(sum(wm),0) FROM bookings WHERE container_id=$1 AND status='booked'`, b.ContainerID).Scan(&wm)
		steps[1].Detail = fmt.Sprintf("%s, %.1f W/M", b.BoxCode, wm)
	}
	if b.Mode == "coload" {
		steps[1].Detail = "co-load partner"
		steps[2] = Step{Title: "Co-load booked", Detail: "partner LCL rate", Done: closed}
	} else if closed {
		steps[2].Detail = fmt.Sprintf("%d forwarders bid", bidders)
	}
	waiting := []string{"Confirm your booking", "Waiting for the plan to lock", "Forwarders are bidding", "Eimsky is choosing the forwarder", "Invoice follows once documents are in"}
	if b.Mode == "coload" {
		waiting[2] = "Co-load being arranged"
	}
	for i := range steps {
		steps[i].Now = waiting[i]
		if !steps[i].Done {
			steps[i].Active = true
			break
		}
	}
	return steps
}

func fmtTime(t time.Time) string { return t.In(domain.Colombo).Format("Mon 2 Jan 15:04") }

// SearchBookings lists bookings for staff with optional text and status filters.
func (a *App) SearchBookings(ctx context.Context, q, status string, sailingID int64, limit int) ([]*Booking, error) {
	q = strings.TrimSpace(q)
	bs, err := a.queryBookings(ctx, a.DB, `($1='' OR b.ref ILIKE '%'||$1||'%' OR c.name ILIKE '%'||$1||'%')
		AND ($2='' OR b.status=$2 OR ($2='unplaced' AND b.status='booked' AND b.mode='pool' AND b.container_id IS NULL) OR ($2='coload' AND b.mode='coload' AND b.status='booked'))
		AND ($3=0 OR b.sailing_id=$3)
		ORDER BY b.created_at DESC LIMIT $4`, q, status, sailingID, limit)
	if err != nil {
		return nil, err
	}
	cache := map[int64]*Sailing{}
	for _, b := range bs {
		if s, ok := cache[b.SailingID]; ok {
			b.Sailing = s
			continue
		}
		if b.Sailing, err = a.SailingByID(ctx, b.SailingID); err != nil {
			return nil, err
		}
		cache[b.SailingID] = b.Sailing
	}
	return bs, nil
}
