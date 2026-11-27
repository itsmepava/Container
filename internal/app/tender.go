package app

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/itsmepava/container/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Tender struct {
	ID           int64
	Ref          string
	ContainerID  int64
	VesselID     int64
	OpensAt      time.Time
	ClosesAt     time.Time
	Status       string
	AwardedBidID int64
	AwardKind    string
	AwardReason  string
	AwardedAt    *time.Time
	HandoverAt   *time.Time

	Container  *Container
	Sailing    *Sailing
	Vessel     *VesselOption
	CBM        float64
	Kg         float64
	WM         float64
	Lots       int
	Categories []domain.Category
	BidCount   int
}

func (t *Tender) StatusLabel() string {
	switch t.Status {
	case "open":
		return "Open"
	case "closed":
		return "Bids closed"
	case "awarded":
		return "Awarded"
	case "cancelled":
		return "Cancelled"
	}
	return t.Status
}

func (t *Tender) CategoryLabel() string {
	var s []string
	for _, c := range t.Categories {
		s = append(s, strings.ToLower(c.Label()))
	}
	if len(s) == 0 {
		return ""
	}
	out := strings.Join(s, ", ")
	return strings.ToUpper(out[:1]) + out[1:]
}

// AcceptingBids reports whether bids can be placed or changed now.
func (t *Tender) AcceptingBids(now time.Time) bool {
	return t.Status == "open" && now.Before(t.ClosesAt)
}

const tenderCols = `t.id,t.ref,t.container_id,t.vessel_id,t.opens_at,t.closes_at,t.status,COALESCE(t.awarded_bid_id,0),t.award_kind,t.award_reason,t.awarded_at,t.handover_at`

func (a *App) loadTender(ctx context.Context, q Querier, id int64) (*Tender, error) {
	t := &Tender{}
	err := q.QueryRow(ctx, `SELECT `+tenderCols+` FROM tenders t WHERE t.id=$1`, id).Scan(&t.ID, &t.Ref, &t.ContainerID, &t.VesselID, &t.OpensAt, &t.ClosesAt, &t.Status, &t.AwardedBidID, &t.AwardKind, &t.AwardReason, &t.AwardedAt, &t.HandoverAt)
	if err != nil {
		return nil, notFound(err)
	}
	if t.Container, err = a.ContainerByID(ctx, t.ContainerID); err != nil {
		return nil, err
	}
	if t.Sailing, err = a.SailingByID(ctx, t.Container.SailingID); err != nil {
		return nil, err
	}
	t.Vessel = t.Sailing.VesselByID(t.VesselID)
	rows, err := q.Query(ctx, `SELECT cbm, kg, wm, category FROM bookings WHERE container_id=$1 AND status='booked' AND mode='pool'`, t.ContainerID)
	if err != nil {
		return nil, err
	}
	cats := map[domain.Category]bool{}
	for rows.Next() {
		var cbm, kg, wm float64
		var c string
		if err := rows.Scan(&cbm, &kg, &wm, &c); err != nil {
			rows.Close()
			return nil, err
		}
		t.CBM += cbm
		t.Kg += kg
		t.WM += wm
		t.Lots++
		cats[domain.Category(c)] = true
	}
	rows.Close()
	for _, c := range domain.Categories {
		if cats[c] {
			t.Categories = append(t.Categories, c)
		}
	}
	t.CBM, t.Kg, t.WM = domain.Round(t.CBM, 2), domain.Round(t.Kg, 1), domain.Round(t.WM, 3)
	_ = q.QueryRow(ctx, `SELECT count(*) FROM bids WHERE tender_id=$1`, id).Scan(&t.BidCount)
	return t, nil
}

type Bid struct {
	ID          int64
	TenderID    int64
	CompanyID   int64
	CompanyName string
	AmountCents int64
	VesselID    int64
	Includes    map[domain.Inclusion]bool
	Note        string
	SubmittedAt time.Time
	UpdatedAt   time.Time
}

func (b *Bid) eval() domain.BidEval {
	return domain.BidEval{ID: b.ID, CompanyID: b.CompanyID, AmountCents: b.AmountCents, VesselID: b.VesselID, Includes: b.Includes, SubmittedAt: b.SubmittedAt}
}

func (a *App) bids(ctx context.Context, q Querier, tenderID int64) ([]*Bid, error) {
	rows, err := q.Query(ctx, `SELECT b.id,b.tender_id,b.company_id,c.name,b.amount_cents,b.vessel_id,b.inc_ocean,b.inc_origin,b.inc_destination,b.inc_destuff,b.note,b.submitted_at,b.updated_at
		FROM bids b JOIN companies c ON c.id=b.company_id WHERE b.tender_id=$1 ORDER BY b.amount_cents, b.submitted_at`, tenderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Bid
	for rows.Next() {
		b := &Bid{}
		var o, or, d, ds bool
		if err := rows.Scan(&b.ID, &b.TenderID, &b.CompanyID, &b.CompanyName, &b.AmountCents, &b.VesselID, &o, &or, &d, &ds, &b.Note, &b.SubmittedAt, &b.UpdatedAt); err != nil {
			return nil, err
		}
		b.Includes = map[domain.Inclusion]bool{domain.IncOcean: o, domain.IncOrigin: or, domain.IncDestination: d, domain.IncDestuff: ds}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ---------- Ops view ----------

type BidRow struct {
	Bid       *Bid
	Vessel    *VesselOption
	Compliant bool
	Reason    string
	AutoPick  bool
	Awarded   bool
}

type BillRow struct {
	domain.ShipperBill
	Ref         string
	CompanyName string
	CBM         float64
	AllInPerWM  float64
}

type TenderView struct {
	Tender        *Tender
	Bids          []BidRow
	AutoPick      *BidRow
	Selected      *BidRow
	Bills         []BillRow
	Summary       domain.BillSummary
	FeePerWM      float64
	HandlingCents int64
	Invited       []string
	Audit         []AuditEntry
	CheaperNote   string
	OverCap       bool
	ExtraCents    int64 // selected vs auto pick
}

// TenderView builds the ops tender screen; selectedBid previews another bid.
func (a *App) TenderView(ctx context.Context, tenderID, selectedBid int64) (*TenderView, error) {
	t, err := a.loadTender(ctx, a.DB, tenderID)
	if err != nil {
		return nil, err
	}
	bs, err := a.bids(ctx, a.DB, tenderID)
	if err != nil {
		return nil, err
	}
	st := a.Settings(ctx)
	v := &TenderView{Tender: t, FeePerWM: st.FeePerWM, HandlingCents: st.HandlingCents}
	var evals []domain.BidEval
	for _, b := range bs {
		evals = append(evals, b.eval())
	}
	sorted, pick := domain.RankBids(evals, t.VesselID)
	byID := map[int64]*Bid{}
	for _, b := range bs {
		byID[b.ID] = b
	}
	for i, e := range sorted {
		b := byID[e.ID]
		ok, why := domain.Compliance(e, t.VesselID)
		row := BidRow{Bid: b, Vessel: t.Sailing.VesselByID(b.VesselID), Compliant: ok, Reason: why, AutoPick: i == pick, Awarded: b.ID == t.AwardedBidID}
		v.Bids = append(v.Bids, row)
	}
	for i := range v.Bids {
		if v.Bids[i].AutoPick {
			v.AutoPick = &v.Bids[i]
		}
	}
	sel := selectedBid
	if sel == 0 && t.AwardedBidID != 0 {
		sel = t.AwardedBidID
	}
	for i := range v.Bids {
		if v.Bids[i].Bid.ID == sel {
			v.Selected = &v.Bids[i]
		}
	}
	if v.Selected == nil {
		v.Selected = v.AutoPick
	}
	if v.Selected != nil {
		v.Bills, v.Summary, err = a.billPreview(ctx, a.DB, t, v.Selected.Bid.AmountCents)
		if err != nil {
			return nil, err
		}
		v.OverCap = v.Summary.AnyOverCap
		if v.AutoPick != nil {
			v.ExtraCents = v.Selected.Bid.AmountCents - v.AutoPick.Bid.AmountCents
		}
	}
	// A cheaper bid on another vessel is worth pointing out.
	if v.AutoPick != nil {
		for _, r := range v.Bids {
			if !r.Compliant && r.Bid.AmountCents < v.AutoPick.Bid.AmountCents && r.Vessel != nil && t.Vessel != nil && r.Vessel.ID != t.Vessel.ID {
				save := v.AutoPick.Bid.AmountCents - r.Bid.AmountCents
				per := 0.0
				if t.WM > 0 {
					per = float64(save) / 100 / t.WM
				}
				days := r.Vessel.ETA.Sub(t.Vessel.ETA).Hours() / 24
				when := fmt.Sprintf("arrives %.0f days later", math.Abs(days))
				if days < 0 {
					when = fmt.Sprintf("arrives %.0f days earlier", -days)
				}
				v.CheaperNote = fmt.Sprintf("Cheaper on another vessel: %s on %s saves USD %s for the box (USD %.2f per CBM) but %s.", r.Bid.CompanyName, r.Vessel.Label(), domain.MoneyShort(save), per, when)
				break
			}
		}
	}
	rows, err := a.DB.Query(ctx, `SELECT c.name FROM tender_invites i JOIN companies c ON c.id=i.company_id WHERE i.tender_id=$1 ORDER BY c.name`, tenderID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var n string
		_ = rows.Scan(&n)
		v.Invited = append(v.Invited, n)
	}
	rows.Close()
	v.Audit, _ = a.tenderAudit(ctx, t)
	return v, nil
}

func (a *App) tenderAudit(ctx context.Context, t *Tender) ([]AuditEntry, error) {
	rows, err := a.DB.Query(ctx, `SELECT id, at, actor_name, action, entity, COALESCE(entity_id,0), summary, detail::text FROM audit_log
		WHERE (entity='tender' AND entity_id=$1) OR (entity='sailing' AND entity_id=$2 AND action LIKE 'plan.%')
		OR (entity='booking' AND action LIKE 'move.%' AND entity_id IN (SELECT id FROM bookings WHERE sailing_id=$2))
		ORDER BY at, id`, t.ID, t.Sailing.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.At, &e.ActorName, &e.Action, &e.Entity, &e.EntityID, &e.Summary, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (a *App) billPreview(ctx context.Context, q Querier, t *Tender, bidCents int64) ([]BillRow, domain.BillSummary, error) {
	bs, err := a.queryBookings(ctx, q, `b.container_id=$1 AND b.status='booked' AND b.mode='pool' ORDER BY b.confirmed_at, b.id`, t.ContainerID)
	if err != nil {
		return nil, domain.BillSummary{}, err
	}
	var in []domain.BillInput
	for _, b := range bs {
		in = append(in, domain.BillInput{BookingID: b.ID, WM: b.WM, CapCents: b.CapCents})
	}
	st := a.Settings(ctx)
	bills := domain.Bill(bidCents, st.FeePerWM, st.HandlingCents, in)
	var rows []BillRow
	for i, b := range bills {
		r := BillRow{ShipperBill: b, Ref: bs[i].Ref, CompanyName: bs[i].CompanyName, CBM: bs[i].CBM}
		if b.WM > 0 {
			r.AllInPerWM = float64(b.FreightCents+b.FeeCents+b.HandlingCents) / 100 / b.WM
		}
		rows = append(rows, r)
	}
	return rows, domain.Summarize(bidCents, bills), nil
}

// TenderList lists tenders for ops, newest first.
func (a *App) TenderList(ctx context.Context, status string) ([]*Tender, error) {
	sql := `SELECT id FROM tenders WHERE ($1='' OR status=$1) ORDER BY CASE status WHEN 'closed' THEN 0 WHEN 'open' THEN 1 ELSE 2 END, closes_at DESC LIMIT 200`
	rows, err := a.DB.Query(ctx, sql, status)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	var out []*Tender
	for _, id := range ids {
		t, err := a.loadTender(ctx, a.DB, id)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// TenderForContainer returns the tender id for a container, if any.
func (a *App) TenderForContainer(ctx context.Context, containerID int64) int64 {
	var id int64
	_ = a.DB.QueryRow(ctx, `SELECT id FROM tenders WHERE container_id=$1`, containerID).Scan(&id)
	return id
}

// ---------- Award ----------

// Award gives the box to a bid. Picking anything but the auto pick is an
// override: it needs a reason, and is blocked if it pushes a shipper above its cap.
func (a *App) Award(ctx context.Context, actor *User, tenderID, bidID int64, reason string) error {
	reason = strings.TrimSpace(reason)
	return a.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT id FROM tenders WHERE id=$1 FOR UPDATE`, tenderID); err != nil {
			return err
		}
		t, err := a.loadTender(ctx, tx, tenderID)
		if err != nil {
			return err
		}
		if t.Status == "awarded" {
			return Errf("This tender is already awarded.")
		}
		if t.Status == "cancelled" {
			return Errf("This tender was cancelled.")
		}
		if t.Status == "open" && a.Now().Before(t.ClosesAt) {
			return Errf("Bids are still open until %s. Wait for them to close, or change the deadline.", fmtTime(t.ClosesAt))
		}
		bs, err := a.bids(ctx, tx, tenderID)
		if err != nil {
			return err
		}
		var evals []domain.BidEval
		var chosen *Bid
		for _, b := range bs {
			evals = append(evals, b.eval())
			if b.ID == bidID {
				chosen = b
			}
		}
		if chosen == nil {
			return Errf("Choose a bid to award.")
		}
		sorted, pick := domain.RankBids(evals, t.VesselID)
		isAuto := pick >= 0 && sorted[pick].ID == bidID
		bills, sum, err := a.billPreview(ctx, tx, t, chosen.AmountCents)
		if err != nil {
			return err
		}
		kind := "auto"
		if !isAuto {
			kind = "override"
			if reason == "" {
				return Errf("Choosing another bidder needs a written reason. It's kept with the tender.")
			}
			if sum.AnyOverCap {
				return Errf("This override would push at least one shipper above its cost cap, so it's blocked.")
			}
		}
		for _, b := range bills {
			if _, err := tx.Exec(ctx, `UPDATE bookings SET freight_cents=$2, fee_cents=$3, handling_cents=$6, total_cents=$4, cap_applied=$5, priced_at=now() WHERE id=$1`,
				b.BookingID, b.FreightCents, b.FeeCents, b.TotalCents, b.CapApplied, b.HandlingCents); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE tenders SET status='awarded', awarded_bid_id=$2, award_kind=$3, award_reason=$4, awarded_by=$5, awarded_at=now(), vessel_id=$6 WHERE id=$1`,
			tenderID, bidID, kind, reason, actor.ID, chosen.VesselID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE containers SET status='awarded' WHERE id=$1`, t.ContainerID); err != nil {
			return err
		}
		if chosen.VesselID != t.VesselID {
			if _, err := tx.Exec(ctx, `UPDATE sailings SET chosen_vessel_id=$2 WHERE id=$1`, t.Sailing.ID, chosen.VesselID); err != nil {
				return err
			}
		}
		var open int
		_ = tx.QueryRow(ctx, `SELECT count(*) FROM tenders t JOIN containers c ON c.id=t.container_id WHERE c.sailing_id=$1 AND t.status IN ('open','closed')`, t.Sailing.ID).Scan(&open)
		if open == 0 {
			if _, err := tx.Exec(ctx, `UPDATE sailings SET status='awarded' WHERE id=$1`, t.Sailing.ID); err != nil {
				return err
			}
		}
		summary := fmt.Sprintf("Awarded %s to %s at USD %s", t.Ref, chosen.CompanyName, domain.MoneyShort(chosen.AmountCents))
		if kind == "override" {
			summary = "Override: " + summary + " · reason: " + reason
		}
		if err := a.Audit(ctx, tx, actor, "tender.award", "tender", tenderID, summary, map[string]any{"bid": bidID, "kind": kind, "reason": reason}); err != nil {
			return err
		}
		// Winner.
		vessel := t.Sailing.VesselByID(chosen.VesselID)
		if err := a.NotifyCompany(ctx, tx, chosen.CompanyID, Note{
			Title: "You won " + t.Ref,
			Body:  fmt.Sprintf("1 × %s %s at USD %s on %s. Upload your booking confirmation; shipper contacts are now in the app.", t.Container.BoxCode, t.Sailing.Lane.Short(), domain.MoneyShort(chosen.AmountCents), vessel.Label()),
			Push:  true, Email: true, Data: map[string]any{"tenderId": tenderID, "screen": "award"},
		}); err != nil {
			return err
		}
		// Other bidders learn the result but not the winning price.
		for _, b := range bs {
			if b.ID == bidID {
				continue
			}
			if err := a.NotifyCompany(ctx, tx, b.CompanyID, Note{Title: t.Ref + " was awarded to another forwarder", Body: "Thanks for bidding. Watch for the next tender on this lane.", Push: true, Data: map[string]any{"tenderId": tenderID}}); err != nil {
				return err
			}
		}
		// Shippers.
		for _, b := range bills {
			var cid int64
			var ref string
			_ = tx.QueryRow(ctx, `SELECT company_id, ref FROM bookings WHERE id=$1`, b.BookingID).Scan(&cid, &ref)
			body := fmt.Sprintf("Your final cost for %s is USD %s (freight %s + Eimsky fee %s).", ref, domain.Money(b.TotalCents), domain.Money(b.FreightCents), domain.Money(b.FeeCents))
			if b.CapApplied {
				body = fmt.Sprintf("Your final cost for %s is USD %s, held at your booking estimate.", ref, domain.Money(b.TotalCents))
			}
			body += fmt.Sprintf(" Carried by %s on %s.", chosen.CompanyName, vessel.Label())
			if err := a.NotifyCompany(ctx, tx, cid, Note{Title: "Your cost is final: USD " + domain.Money(b.TotalCents), Body: body, Link: fmt.Sprintf("/f/bookings/%d", b.BookingID), Email: true}); err != nil {
				return err
			}
		}
		return nil
	})
}

// ExtendTender sets a new closing time and reopens bidding if it had closed.
func (a *App) ExtendTender(ctx context.Context, actor *User, tenderID int64, closes time.Time) error {
	if !closes.After(a.Now().Add(15 * time.Minute)) {
		return Errf("Choose a closing time at least 15 minutes from now.")
	}
	return a.Tx(ctx, func(tx pgx.Tx) error {
		t, err := a.loadTender(ctx, tx, tenderID)
		if err != nil {
			return err
		}
		if t.Status != "open" && t.Status != "closed" {
			return Errf("Only open or closed tenders can be extended.")
		}
		if closes.After(t.Sailing.CFSCutoff) {
			return Errf("The new deadline is after the CFS cut-off (%s).", fmtTime(t.Sailing.CFSCutoff))
		}
		if _, err := tx.Exec(ctx, `UPDATE tenders SET closes_at=$2, status='open' WHERE id=$1`, tenderID, closes); err != nil {
			return err
		}
		verb := "Deadline extended"
		if t.Status == "closed" {
			verb = "Tender reopened"
		}
		if err := a.Audit(ctx, tx, actor, "tender.extend", "tender", tenderID, fmt.Sprintf("%s to %s", verb, fmtTime(closes)), nil); err != nil {
			return err
		}
		ids, err := usersOf(ctx, tx, `company_id IN (SELECT company_id FROM tender_invites WHERE tender_id=$1)`, tenderID)
		if err != nil {
			return err
		}
		return a.Notify(ctx, tx, ids, Note{Title: verb + ": " + t.Ref, Body: "Bids now close " + fmtTime(closes) + ".", Push: true, Data: map[string]any{"tenderId": tenderID}})
	})
}

// InviteForwarders adds forwarders to a tender and notifies them.
func (a *App) InviteForwarders(ctx context.Context, actor *User, tenderID int64, companyIDs []int64) (int, error) {
	n := 0
	err := a.Tx(ctx, func(tx pgx.Tx) error {
		t, err := a.loadTender(ctx, tx, tenderID)
		if err != nil {
			return err
		}
		if !t.AcceptingBids(a.Now()) {
			return Errf("Reopen the tender before inviting more forwarders.")
		}
		for _, cid := range companyIDs {
			tag, err := tx.Exec(ctx, `INSERT INTO tender_invites(tender_id,company_id) SELECT $1,id FROM companies WHERE id=$2 AND kind='forwarder' AND status='approved' ON CONFLICT DO NOTHING`, tenderID, cid)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 1 {
				n++
				if err := a.NotifyCompany(ctx, tx, cid, Note{Title: fmt.Sprintf("New tender: 1 × %s %s", t.Container.BoxCode, t.Sailing.Lane.Short()),
					Body: fmt.Sprintf("%s · %.1f CBM. Bids close %s.", t.Sailing.Label(), t.CBM, fmtTime(t.ClosesAt)), Push: true, Email: true, Data: map[string]any{"tenderId": tenderID}}); err != nil {
					return err
				}
			}
		}
		return a.Audit(ctx, tx, actor, "tender.invite", "tender", tenderID, fmt.Sprintf("Invited %d more forwarder(s)", n), nil)
	})
	return n, err
}

// CloseDueTenders closes tenders whose deadline has passed.
func (a *App) CloseDueTenders(ctx context.Context) (int, error) {
	rows, err := a.DB.Query(ctx, `SELECT id FROM tenders WHERE status='open' AND closes_at <= $1`, a.Now())
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		err := a.Tx(ctx, func(tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, `UPDATE tenders SET status='closed' WHERE id=$1 AND status='open'`, id)
			if err != nil || tag.RowsAffected() == 0 {
				return err
			}
			t, err := a.loadTender(ctx, tx, id)
			if err != nil {
				return err
			}
			if err := a.Audit(ctx, tx, nil, "tender.close", "tender", id, fmt.Sprintf("Bids closed: %d sealed bid(s)", t.BidCount), nil); err != nil {
				return err
			}
			if err := a.NotifyStaff(ctx, tx, Note{Title: fmt.Sprintf("Bids closed on %s: %d bid(s)", t.Ref, t.BidCount), Body: "Review and award the box.", Link: fmt.Sprintf("/ops/tenders/%d", id), Email: true}); err != nil {
				return err
			}
			ids, err := usersOf(ctx, tx, `company_id IN (SELECT company_id FROM bids WHERE tender_id=$1)`, id)
			if err != nil {
				return err
			}
			return a.Notify(ctx, tx, ids, Note{Title: "Bidding closed on " + t.Ref, Body: "The result will follow soon.", Push: true, Data: map[string]any{"tenderId": id}})
		})
		if err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

// ---------- Forwarder side ----------

// ForwarderTender is a tender as a forwarder sees it: no shipper names or prices.
type ForwarderTender struct {
	*Tender
	MyBid   *Bid
	Result  string // "", "won", "lost"
	Vessels []*VesselOption
	Invited bool
}

func (a *App) forwarderCanSee(ctx context.Context, u *User, tenderID int64) bool {
	var ok bool
	_ = a.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tender_invites WHERE tender_id=$1 AND company_id=$2)
		OR EXISTS(SELECT 1 FROM bids WHERE tender_id=$1 AND company_id=$2)`, tenderID, u.CompanyID).Scan(&ok)
	return ok
}

func (a *App) myBid(ctx context.Context, tenderID, companyID int64) *Bid {
	bs, err := a.bids(ctx, a.DB, tenderID)
	if err != nil {
		return nil
	}
	for _, b := range bs {
		if b.CompanyID == companyID {
			return b
		}
	}
	return nil
}

// ForwarderTenderByID loads one tender for a forwarder.
func (a *App) ForwarderTenderByID(ctx context.Context, u *User, id int64) (*ForwarderTender, error) {
	if !u.IsForwarder() || !a.forwarderCanSee(ctx, u, id) {
		return nil, ErrNotFound
	}
	t, err := a.loadTender(ctx, a.DB, id)
	if err != nil {
		return nil, err
	}
	ft := &ForwarderTender{Tender: t, MyBid: a.myBid(ctx, id, u.CompanyID), Vessels: t.Sailing.Vessels, Invited: true}
	if t.Status == "awarded" && ft.MyBid != nil {
		ft.Result = "lost"
		if ft.MyBid.ID == t.AwardedBidID {
			ft.Result = "won"
		}
	}
	return ft, nil
}

// ForwarderTenders lists tenders for a forwarder: "open", "mine" or "awarded".
func (a *App) ForwarderTenders(ctx context.Context, u *User, scope string) ([]*ForwarderTender, error) {
	var sql string
	switch scope {
	case "open":
		sql = `SELECT t.id FROM tenders t JOIN tender_invites i ON i.tender_id=t.id AND i.company_id=$1 WHERE t.status='open' AND t.closes_at > now() ORDER BY t.closes_at`
	case "mine":
		sql = `SELECT t.id FROM tenders t JOIN bids b ON b.tender_id=t.id AND b.company_id=$1 ORDER BY b.updated_at DESC LIMIT 100`
	case "awarded":
		sql = `SELECT t.id FROM tenders t JOIN bids b ON b.id=t.awarded_bid_id WHERE b.company_id=$1 ORDER BY t.awarded_at DESC LIMIT 100`
	default:
		return nil, Errf("unknown list")
	}
	rows, err := a.DB.Query(ctx, sql, u.CompanyID)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	var out []*ForwarderTender
	for _, id := range ids {
		ft, err := a.ForwarderTenderByID(ctx, u, id)
		if err != nil {
			return nil, err
		}
		out = append(out, ft)
	}
	return out, nil
}

type BidInput struct {
	TenderID    int64
	AmountCents int64
	VesselID    int64
	Includes    map[domain.Inclusion]bool
	Note        string
}

// PlaceBid creates or updates the forwarder's sealed bid.
func (a *App) PlaceBid(ctx context.Context, u *User, in BidInput) (*Bid, error) {
	if !u.IsForwarder() {
		return nil, ErrForbidden
	}
	if u.CompanyID == 0 || u.CompanyStatus != "approved" {
		return nil, Errf("Your company is waiting for Eimsky's approval. You can bid once it's approved.")
	}
	if in.AmountCents <= 0 {
		return nil, Errf("Enter your all-in price per container.")
	}
	if in.AmountCents > 100_000_00 {
		return nil, Errf("That price looks too high. Check the amount.")
	}
	ft, err := a.ForwarderTenderByID(ctx, u, in.TenderID)
	if err != nil {
		return nil, err
	}
	if !ft.AcceptingBids(a.Now()) {
		return nil, Errf("Bidding on %s has closed.", ft.Ref)
	}
	if in.VesselID == 0 {
		in.VesselID = ft.VesselID
	}
	if ft.Sailing.VesselByID(in.VesselID) == nil {
		return nil, Errf("Choose a vessel from this sailing.")
	}
	if len(strings.TrimSpace(in.Note)) > 500 {
		return nil, Errf("Keep the note under 500 characters.")
	}
	inc := func(i domain.Inclusion) bool { return in.Includes[i] }
	err = a.Tx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO bids(tender_id,company_id,user_id,amount_cents,vessel_id,inc_ocean,inc_origin,inc_destination,inc_destuff,note)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (tender_id,company_id) DO UPDATE SET user_id=EXCLUDED.user_id, amount_cents=EXCLUDED.amount_cents, vessel_id=EXCLUDED.vessel_id,
			inc_ocean=EXCLUDED.inc_ocean, inc_origin=EXCLUDED.inc_origin, inc_destination=EXCLUDED.inc_destination, inc_destuff=EXCLUDED.inc_destuff, note=EXCLUDED.note, updated_at=now()`,
			in.TenderID, u.CompanyID, u.ID, in.AmountCents, in.VesselID, inc(domain.IncOcean), inc(domain.IncOrigin), inc(domain.IncDestination), inc(domain.IncDestuff), strings.TrimSpace(in.Note))
		if err != nil {
			return err
		}
		// The price stays sealed: the audit log records that a bid was placed, not its amount.
		return a.Audit(ctx, tx, u, "bid.place", "tender", in.TenderID, u.CompanyName+" placed or updated a sealed bid", nil)
	})
	if err != nil {
		return nil, err
	}
	return a.myBid(ctx, in.TenderID, u.CompanyID), nil
}

// WithdrawBid removes the forwarder's bid while the tender is open.
func (a *App) WithdrawBid(ctx context.Context, u *User, tenderID int64) error {
	ft, err := a.ForwarderTenderByID(ctx, u, tenderID)
	if err != nil {
		return err
	}
	if !ft.AcceptingBids(a.Now()) {
		return Errf("Bidding has closed, so the bid can't be withdrawn.")
	}
	_, err = a.DB.Exec(ctx, `DELETE FROM bids WHERE tender_id=$1 AND company_id=$2`, tenderID, u.CompanyID)
	if err == nil {
		_ = a.Audit(ctx, a.DB, u, "bid.withdraw", "tender", tenderID, u.CompanyName+" withdrew its bid", nil)
	}
	return err
}

// AwardShipper is what the winning forwarder sees about each shipper.
type AwardShipper struct {
	Booking      *Booking
	ContactName  string
	ContactEmail string
	ContactPhone string
	Docs         []*Document
}

type AwardDetail struct {
	Tender       *ForwarderTender
	Shippers     []AwardShipper
	Confirmation *Document
	BLCopy       *Document
	OriginCFS    string
}

// AwardDetailFor returns shipper contacts and documents to the winning forwarder.
func (a *App) AwardDetailFor(ctx context.Context, u *User, tenderID int64) (*AwardDetail, error) {
	ft, err := a.ForwarderTenderByID(ctx, u, tenderID)
	if err != nil {
		return nil, err
	}
	if ft.Result != "won" {
		return nil, ErrNotFound
	}
	d := &AwardDetail{Tender: ft, OriginCFS: ft.Sailing.Lane.OriginCFS}
	bs, err := a.queryBookings(ctx, a.DB, `b.container_id=$1 AND b.status='booked' ORDER BY b.confirmed_at, b.id`, ft.ContainerID)
	if err != nil {
		return nil, err
	}
	for _, b := range bs {
		s := AwardShipper{Booking: b}
		_ = a.DB.QueryRow(ctx, `SELECT name, email, phone FROM users WHERE company_id=$1 AND active ORDER BY (role='factory_admin') DESC, id LIMIT 1`, b.CompanyID).Scan(&s.ContactName, &s.ContactEmail, &s.ContactPhone)
		docs, err := a.DocsFor(ctx, "booking", b.ID)
		if err != nil {
			return nil, err
		}
		s.Docs = docs
		d.Shippers = append(d.Shippers, s)
	}
	tds, err := a.DocsFor(ctx, "tender", tenderID)
	if err != nil {
		return nil, err
	}
	for _, x := range tds {
		switch x.Kind {
		case "booking_confirmation":
			d.Confirmation = x
		case "bl_copy":
			d.BLCopy = x
		}
	}
	return d, nil
}

// CloseTenderNow ends bidding early, e.g. once every invited forwarder has bid.
func (a *App) CloseTenderNow(ctx context.Context, actor *User, tenderID int64) error {
	t, err := a.loadTender(ctx, a.DB, tenderID)
	if err != nil {
		return err
	}
	if t.Status != "open" {
		return Errf("Bidding on %s isn't open.", t.Ref)
	}
	if t.BidCount == 0 {
		return Errf("There are no bids yet. Extend the deadline or invite more forwarders instead.")
	}
	if _, err := a.DB.Exec(ctx, `UPDATE tenders SET closes_at=$2 WHERE id=$1 AND status='open'`, tenderID, a.Now()); err != nil {
		return err
	}
	_ = a.Audit(ctx, a.DB, actor, "tender.close_now", "tender", tenderID, "Bidding closed early by "+actor.Name, nil)
	_, err = a.CloseDueTenders(ctx)
	return err
}

// FallbackOffer describes the pre-agreed rate available for a tender nobody bid on.
type FallbackOffer struct {
	CompanyID   int64
	CompanyName string
	AmountCents int64
}

// Fallback returns the lane's pre-agreed forwarder and rate for this tender's box, if set.
func (a *App) Fallback(ctx context.Context, t *Tender) *FallbackOffer {
	l := t.Sailing.Lane
	rate := l.Fallback[t.Container.BoxCode]
	if l.FallbackID == 0 || rate <= 0 {
		return nil
	}
	return &FallbackOffer{CompanyID: l.FallbackID, CompanyName: l.FallbackName, AmountCents: domain.Cents(rate)}
}

// AwardFallback awards a closed tender that got no compliant bid to the lane's
// pre-agreed forwarder at the pre-agreed rate.
func (a *App) AwardFallback(ctx context.Context, actor *User, tenderID int64) error {
	t, err := a.loadTender(ctx, a.DB, tenderID)
	if err != nil {
		return err
	}
	if t.Status != "closed" {
		return Errf("Use the fallback rate only after bidding closes with no usable bid.")
	}
	fb := a.Fallback(ctx, t)
	if fb == nil {
		return Errf("This lane has no pre-agreed forwarder and rate for a %s. Set them in Lanes and sailings.", t.Container.BoxCode)
	}
	var uid int64
	if err := a.DB.QueryRow(ctx, `SELECT id FROM users WHERE company_id=$1 AND active ORDER BY id LIMIT 1`, fb.CompanyID).Scan(&uid); err != nil {
		return Errf("%s has no active user to award to.", fb.CompanyName)
	}
	var bidID int64
	err = a.DB.QueryRow(ctx, `INSERT INTO bids(tender_id,company_id,user_id,amount_cents,vessel_id,inc_ocean,inc_origin,inc_destination,inc_destuff,note)
		VALUES ($1,$2,$3,$4,$5,true,true,true,true,'Pre-agreed fallback rate')
		ON CONFLICT (tender_id,company_id) DO UPDATE SET amount_cents=EXCLUDED.amount_cents, vessel_id=EXCLUDED.vessel_id, inc_ocean=true, inc_origin=true, inc_destination=true, inc_destuff=true, note=EXCLUDED.note, updated_at=now()
		RETURNING id`, tenderID, fb.CompanyID, uid, fb.AmountCents, t.VesselID).Scan(&bidID)
	if err != nil {
		return err
	}
	if err := a.Award(ctx, actor, tenderID, bidID, "No usable bids: pre-agreed fallback rate"); err != nil {
		return err
	}
	_, err = a.DB.Exec(ctx, `UPDATE tenders SET award_kind='fallback' WHERE id=$1`, tenderID)
	return err
}

// ColoadTender gives up on a tender and sends its lots to the co-load partner.
func (a *App) ColoadTender(ctx context.Context, actor *User, tenderID int64) error {
	return a.Tx(ctx, func(tx pgx.Tx) error {
		t, err := a.loadTender(ctx, tx, tenderID)
		if err != nil {
			return err
		}
		if t.Status != "closed" && t.Status != "open" {
			return Errf("This tender is already %s.", strings.ToLower(t.StatusLabel()))
		}
		bs, err := a.queryBookings(ctx, tx, `b.container_id=$1 AND b.status='booked' AND b.mode='pool'`, t.ContainerID)
		if err != nil {
			return err
		}
		for _, b := range bs {
			if err := a.priceCoload(ctx, tx, b, t.Sailing); err != nil {
				return err
			}
			if err := a.NotifyCompany(ctx, tx, b.CompanyID, Note{Title: "Your cost for " + b.Ref + " is set",
				Body: "No forwarder bid on your container, so your lot goes with our co-load partner on the same sailing. Your cost cap still applies.",
				Link: fmt.Sprintf("/f/bookings/%d", b.ID), Email: true}); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE tenders SET status='cancelled', award_kind='coload', awarded_by=$2, awarded_at=now() WHERE id=$1`, tenderID, actor.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE containers SET status='cancelled' WHERE id=$1`, t.ContainerID); err != nil {
			return err
		}
		var open int
		_ = tx.QueryRow(ctx, `SELECT count(*) FROM tenders t JOIN containers c ON c.id=t.container_id WHERE c.sailing_id=$1 AND t.status IN ('open','closed')`, t.Sailing.ID).Scan(&open)
		if open == 0 {
			if _, err := tx.Exec(ctx, `UPDATE sailings SET status=CASE WHEN EXISTS (SELECT 1 FROM tenders t JOIN containers c ON c.id=t.container_id WHERE c.sailing_id=$1 AND t.status='awarded') THEN 'awarded' ELSE 'coload' END WHERE id=$1`, t.Sailing.ID); err != nil {
				return err
			}
		}
		return a.Audit(ctx, tx, actor, "tender.coload", "tender", tenderID, fmt.Sprintf("%s: no usable bids, %d lot(s) sent to the co-load partner", t.Ref, len(bs)), nil)
	})
}

// ---------- Payables ----------

// Payable is what Eimsky owes the winning forwarder for one container.
type Payable struct {
	TenderID      int64
	Ref           string
	Forwarder     string
	Lane          string
	Week          int
	BoxCode       string
	AmountCents   int64
	AwardedAt     *time.Time
	Confirmation  bool
	BLCopy        bool
	PaidAt        *time.Time
	PaidRef       string
	InvoicedCents int64
}

// Payables lists awarded tenders and what Eimsky owes each forwarder.
func (a *App) Payables(ctx context.Context, status string) ([]Payable, error) {
	rows, err := a.DB.Query(ctx, `
		SELECT t.id, t.ref, c.name, o.name || ' → ' || d.name, s.week, ct.box_code, w.amount_cents, t.awarded_at,
		  EXISTS(SELECT 1 FROM documents WHERE owner_kind='tender' AND owner_id=t.id AND kind='booking_confirmation'),
		  EXISTS(SELECT 1 FROM documents WHERE owner_kind='tender' AND owner_id=t.id AND kind='bl_copy'),
		  t.payout_paid_at, t.payout_ref,
		  COALESCE((SELECT sum(i.total_cents) FROM invoices i JOIN bookings b ON b.id=i.booking_id WHERE b.container_id=t.container_id AND i.status<>'void'),0)
		FROM tenders t JOIN bids w ON w.id=t.awarded_bid_id JOIN companies c ON c.id=w.company_id
		JOIN containers ct ON ct.id=t.container_id JOIN sailings s ON s.id=ct.sailing_id JOIN lanes l ON l.id=s.lane_id
		JOIN ports o ON o.code=l.origin JOIN ports d ON d.code=l.dest
		WHERE t.status='awarded' AND ($1='' OR ($1='due' AND t.payout_paid_at IS NULL) OR ($1='paid' AND t.payout_paid_at IS NOT NULL))
		ORDER BY t.payout_paid_at IS NOT NULL, t.awarded_at DESC`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Payable
	for rows.Next() {
		var p Payable
		if err := rows.Scan(&p.TenderID, &p.Ref, &p.Forwarder, &p.Lane, &p.Week, &p.BoxCode, &p.AmountCents, &p.AwardedAt, &p.Confirmation, &p.BLCopy, &p.PaidAt, &p.PaidRef, &p.InvoicedCents); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// MarkPayout records (or undoes) Eimsky's payment to the winning forwarder.
func (a *App) MarkPayout(ctx context.Context, actor *User, tenderID int64, paid bool, ref string) error {
	ref = strings.TrimSpace(ref)
	return a.Tx(ctx, func(tx pgx.Tx) error {
		var tag pgconn.CommandTag
		var err error
		if paid {
			tag, err = tx.Exec(ctx, `UPDATE tenders SET payout_paid_at=now(), payout_ref=$2 WHERE id=$1 AND status='awarded'`, tenderID, ref)
		} else {
			tag, err = tx.Exec(ctx, `UPDATE tenders SET payout_paid_at=NULL, payout_ref='' WHERE id=$1 AND status='awarded'`, tenderID)
		}
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		sum := "Forwarder payment recorded"
		if ref != "" {
			sum += " · ref " + ref
		}
		if !paid {
			sum = "Forwarder payment undone"
		}
		return a.Audit(ctx, tx, actor, "payout", "tender", tenderID, sum, nil)
	})
}
