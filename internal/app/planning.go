package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/itsmepava/container/internal/domain"
	"github.com/jackc/pgx/v5"
)

// loadBoxes builds domain boxes for a sailing's planning containers with the
// booked lots already placed in them.
func (a *App) loadBoxes(ctx context.Context, q Querier, sailingID int64) ([]*domain.Box, []*Container, error) {
	st := a.Settings(ctx)
	cs, err := a.Containers(ctx, q, sailingID)
	if err != nil {
		return nil, nil, err
	}
	var boxes []*domain.Box
	idx := map[int64]*domain.Box{}
	for _, c := range cs {
		b := domain.NewBox(c.Spec, c.Grade, st.Stowage)
		b.Locked = c.Status != "planning"
		boxes = append(boxes, b)
		idx[c.ID] = b
	}
	bs, err := a.queryBookings(ctx, q, `b.sailing_id=$1 AND b.status='booked' AND b.mode='pool' AND b.container_id IS NOT NULL ORDER BY b.confirmed_at, b.id`, sailingID)
	if err != nil {
		return nil, nil, err
	}
	for _, bk := range bs {
		if box, ok := idx[bk.ContainerID]; ok {
			box.Lots = append(box.Lots, domain.Lot{ID: bk.ID, CBM: bk.CBM, Kg: bk.Kg, Category: bk.Category})
		}
	}
	return boxes, cs, nil
}

// LotLabel turns 0,1,2… into A, B, … Z, AA, AB.
func LotLabel(i int) string {
	s := ""
	for i >= 0 {
		s = string(rune('A'+i%26)) + s
		i = i/26 - 1
	}
	return s
}

type PlannedLot struct {
	Booking     *Booking
	Label       string
	Color       int
	MissingDocs []string
	Reason      string // why it is unplaced
	OutOfBest   string // why it doesn't fit the best container option
	PendingMove *MoveRequest
}

func (l PlannedLot) DocsComplete() bool { return len(l.MissingDocs) == 0 }

type PlannedBox struct {
	Container *Container
	Box       *domain.Box
	Lots      []PlannedLot
	Tender    *TenderBrief
}

func (p *PlannedBox) FreePctOfBox() float64 {
	if p.Box.Usable <= 0 {
		return 0
	}
	return p.Box.FreeCBM() / p.Box.Usable * 100
}

type TenderBrief struct {
	ID       int64
	Ref      string
	Status   string
	ClosesAt time.Time
	Bids     int
}

type MoveRequest struct {
	ID          int64
	BookingID   int64
	Target      string
	ToSailingID int64
	Reason      string
	Status      string
	CreatedAt   time.Time
}

func (m *MoveRequest) TargetLabel() string {
	switch m.Target {
	case "next_week":
		return "the next sailing"
	case "extra_box":
		return "an extra container on the same sailing"
	case "coload":
		return "a co-load partner on the same sailing"
	}
	return m.Target
}

type Planner struct {
	Sailing      *Sailing
	Boxes        []*PlannedBox
	Unplaced     []PlannedLot
	CoLoaded     []PlannedLot
	Held         []*Booking
	All          []PlannedLot
	RequestedCBM float64
	UsableCBM    float64
	Options      []domain.ContainerOption
	PoolOptions  []domain.PoolOption
	BreakEvenCBM float64
	BreakEvenPct float64
	NextSailing  *Sailing
	Blockers     []string
	Warnings     []string
	Specs        []domain.ContainerSpec
	ColoadRate   float64
	ExpectedBid  float64
	OwnPerCBM    float64 // expected freight per CBM for what is in the first box now
	Threshold    float64 // own-box threshold, % fill
	PrimaryFill  float64 // % fill of the first box
	Below        bool    // first box is below the own-box threshold
	Best         *domain.ContainerOption
	ColorOf      map[int64]int // booking ID → lot colour
	Bookings     int
	TotalKg      float64
	TotalWM      float64
}

// BreakEvenFor is the CBM at which a box of this type beats co-load.
func (p *Planner) BreakEvenFor(code string) float64 {
	be := domain.BreakEvenCBM(p.Sailing.Lane.Rates[code], p.ColoadRate)
	if be != be || be > 1e6 { // NaN or no co-load rate
		return 0
	}
	return domain.Round(be, 1)
}

// MaxLen is the longest container type, so drawings share one scale.
func (p *Planner) MaxLen() float64 {
	m := 0.0
	for _, s := range p.Specs {
		if l, _, _ := s.Dims(); l > m {
			m = l
		}
	}
	return m
}

// Saving is what an option saves against co-loading what it carries.
func (p *Planner) Saving(o domain.ContainerOption) float64 { return o.SavingVsCoload(p.ColoadRate) }

// BoxSaving is what a planning container saves against co-loading its lots.
func (p *Planner) BoxSaving(pb *PlannedBox) float64 {
	return domain.Round(pb.Box.UsedCBM()*p.ColoadRate-p.Sailing.Lane.Rates[pb.Container.BoxCode], 0)
}

// Badge labels a load: the best option, profitable (above the own-box
// threshold and cheaper than co-load), cheaper than co-load, or below break-even.
type Badge struct{ Label, Class string }

func (p *Planner) badge(fill, saving float64, best bool) Badge {
	switch {
	case best && saving > 0:
		return Badge{"Best fit", "best"}
	case saving > 0 && fill >= p.Threshold:
		return Badge{"Profitable", "green"}
	case saving > 0:
		return Badge{"Cheaper than co-load", "blue"}
	case best:
		return Badge{"Best fit · below break-even", "amber"}
	}
	return Badge{"Below break-even", "amber"}
}

func (p *Planner) OptionBadge(o domain.ContainerOption) Badge {
	return p.badge(o.FillPct, p.Saving(o), o.Best)
}

func (p *Planner) BoxBadge(pb *PlannedBox) Badge {
	return p.badge(pb.Box.FillPct(), p.BoxSaving(pb), false)
}

// SwitchTo is the container type ops should switch the first box to, if the
// best option differs from it and the plan is still open.
func (p *Planner) SwitchTo() string {
	pb := p.PrimaryBox()
	if p.Best == nil || pb == nil || p.Sailing.Status != "open" || pb.Box.Locked {
		return ""
	}
	if p.Best.Spec.Code == pb.Container.BoxCode {
		return ""
	}
	return p.Best.Spec.Code
}

func (p *Planner) CanLock() bool { return len(p.Blockers) == 0 }

func (p *Planner) PrimaryBox() *PlannedBox {
	if len(p.Boxes) == 0 {
		return nil
	}
	return p.Boxes[0]
}

// Planner builds the load planner view for a sailing.
func (a *App) Planner(ctx context.Context, sailingID int64) (*Planner, error) {
	s, err := a.SailingByID(ctx, sailingID)
	if err != nil {
		return nil, err
	}
	st := a.Settings(ctx)
	specs, err := a.ContainerSpecs(ctx)
	if err != nil {
		return nil, err
	}
	p := &Planner{Sailing: s, Specs: specs, ColoadRate: s.Lane.ColoadRate, ColorOf: map[int64]int{}}
	cs, err := a.Containers(ctx, a.DB, sailingID)
	if err != nil {
		return nil, err
	}
	bs, err := a.queryBookings(ctx, a.DB, `b.sailing_id=$1 AND b.status IN ('booked','held') ORDER BY COALESCE(b.confirmed_at,b.created_at), b.id`, sailingID)
	if err != nil {
		return nil, err
	}
	missing, err := a.MissingDocs(ctx, bs)
	if err != nil {
		return nil, err
	}
	moves, err := a.pendingMoves(ctx, sailingID)
	if err != nil {
		return nil, err
	}
	boxByID := map[int64]*PlannedBox{}
	for _, c := range cs {
		pb := &PlannedBox{Container: c, Box: domain.NewBox(c.Spec, c.Grade, st.Stowage)}
		pb.Box.Locked = c.Status != "planning"
		p.Boxes = append(p.Boxes, pb)
		boxByID[c.ID] = pb
		p.UsableCBM += pb.Box.Usable
	}
	var lots []domain.Lot
	n := 0
	for _, b := range bs {
		if b.Status == "held" {
			if b.Active(a.Now()) {
				p.Held = append(p.Held, b)
			}
			continue
		}
		pl := PlannedLot{Booking: b, Label: LotLabel(n), Color: n % 10, MissingDocs: missing[b.ID], PendingMove: moves[b.ID]}
		n++
		p.ColorOf[b.ID] = pl.Color
		p.Bookings++
		p.TotalKg += b.Kg
		p.TotalWM += b.WM
		p.All = append(p.All, pl)
		if b.Mode == "coload" {
			p.CoLoaded = append(p.CoLoaded, pl)
			continue
		}
		p.RequestedCBM += b.CBM
		lots = append(lots, domain.Lot{ID: b.ID, Label: pl.Label, Name: b.CompanyName, CBM: b.CBM, Kg: b.Kg, Category: b.Category, Order: len(lots)})
		if pb, ok := boxByID[b.ContainerID]; ok {
			pb.Box.Lots = append(pb.Box.Lots, domain.Lot{ID: b.ID, Label: pl.Label, CBM: b.CBM, Kg: b.Kg, Category: b.Category})
			pb.Lots = append(pb.Lots, pl)
		} else {
			p.Unplaced = append(p.Unplaced, pl)
		}
	}
	// Explain why each unplaced lot doesn't fit.
	for i, u := range p.Unplaced {
		reason := "not placed yet"
		for _, pb := range p.Boxes {
			if pb.Box.Locked {
				continue
			}
			if err := pb.Box.CanPlace(domain.Lot{CBM: u.Booking.CBM, Kg: u.Booking.Kg, Category: u.Booking.Category}); err != nil {
				reason = err.Error()
			} else {
				reason = fmt.Sprintf("fits Container %d", pb.Container.Seq)
				break
			}
		}
		if len(p.Boxes) == 0 {
			reason = "no container on this sailing"
		}
		p.Unplaced[i].Reason = reason
	}
	p.RequestedCBM = domain.Round(p.RequestedCBM, 2)
	p.UsableCBM = domain.Round(p.UsableCBM, 2)

	grade := s.Lane.DefaultGrade
	ownCode := s.Lane.DefaultBox
	if len(cs) > 0 {
		grade, ownCode = cs[0].Grade, cs[0].BoxCode
	}
	p.Options = domain.ContainerOptions(specs, grade, st.Stowage, lots, s.Lane.Rates)
	for i := range p.Options {
		if p.Options[i].Best {
			p.Best = &p.Options[i]
		}
	}
	if p.Best != nil {
		why := map[int64]string{}
		for _, u := range p.Best.Unplaced {
			why[u.Lot.ID] = u.Reason
		}
		for i := range p.All {
			p.All[i].OutOfBest = why[p.All[i].Booking.ID]
		}
	}
	p.TotalWM = domain.Round(p.TotalWM, 2)
	p.PoolOptions = domain.PoolOptions(specs, st.Stowage, p.RequestedCBM, ownCode, s.Lane.Rates, s.Lane.ColoadRate)
	p.ExpectedBid = s.Lane.Rates[ownCode]
	p.BreakEvenCBM = domain.BreakEvenCBM(p.ExpectedBid, s.Lane.ColoadRate)
	if pb := p.PrimaryBox(); pb != nil && pb.Box.UsedCBM() > 0 {
		p.OwnPerCBM = p.ExpectedBid / pb.Box.UsedCBM()
	}
	if spec, ok := domain.SpecByCode(specs, ownCode); ok {
		p.BreakEvenPct = p.BreakEvenCBM / spec.UsableCBM(st.Stowage) * 100
	}

	// Tenders for locked containers.
	rows, err := a.DB.Query(ctx, `SELECT t.id,t.ref,t.status,t.closes_at,t.container_id,(SELECT count(*) FROM bids WHERE tender_id=t.id)
		FROM tenders t JOIN containers c ON c.id=t.container_id WHERE c.sailing_id=$1`, sailingID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		tb := &TenderBrief{}
		var cid int64
		if err := rows.Scan(&tb.ID, &tb.Ref, &tb.Status, &tb.ClosesAt, &cid, &tb.Bids); err != nil {
			rows.Close()
			return nil, err
		}
		if pb, ok := boxByID[cid]; ok {
			pb.Tender = tb
		}
	}
	rows.Close()

	if next, err := a.nextSailing(ctx, s); err == nil {
		p.NextSailing = next
	}

	// Own-box threshold: below it, recommend co-load, a smaller box or rolling over.
	p.Threshold = st.OwnBoxThreshold
	if pb := p.PrimaryBox(); pb != nil && pb.Box.Usable > 0 {
		p.PrimaryFill = p.RequestedCBM / pb.Box.Usable * 100
	}
	p.Below = p.RequestedCBM > 0 && p.PrimaryFill < p.Threshold
	if p.NextSailing != nil && p.RequestedCBM > 0 {
		p.PoolOptions = append(p.PoolOptions, domain.PoolOption{Key: "roll", Title: "Roll to " + p.NextSailing.Label(),
			Detail: "Every lot moves to the next sailing; shippers are notified and their cost caps stay"})
	}
	if p.Below {
		best := -1
		for i := range p.PoolOptions {
			p.PoolOptions[i].Recommended = false
			if p.PoolOptions[i].Key == "own" || p.PoolOptions[i].Key == "roll" {
				continue
			}
			if best < 0 || p.PoolOptions[i].PerCBM < p.PoolOptions[best].PerCBM {
				best = i
			}
		}
		if best >= 0 {
			p.PoolOptions[best].Recommended = true
		}
	}

	// Lock checklist.
	if s.Status != "open" {
		p.Blockers = append(p.Blockers, "The FCL is already finalized ("+strings.ToLower(s.StatusLabel())+").")
	}
	if s.Vessel() == nil {
		p.Blockers = append(p.Blockers, "Choose a vessel.")
	}
	if len(p.Unplaced) > 0 {
		p.Blockers = append(p.Blockers, fmt.Sprintf("%d lot(s) unplaced: move or co-load them.", len(p.Unplaced)))
	}
	pending := 0
	for _, m := range moves {
		if m != nil {
			pending++
		}
	}
	if pending > 0 {
		p.Blockers = append(p.Blockers, fmt.Sprintf("%d move request(s) waiting for the shipper's answer.", pending))
	}
	filled := 0
	for _, pb := range p.Boxes {
		if len(pb.Lots) > 0 {
			filled++
		}
		for _, c := range pb.Box.Checks() {
			if !c.OK {
				p.Blockers = append(p.Blockers, fmt.Sprintf("Container %d: %s", pb.Container.Seq, c.Label))
			}
		}
	}
	if filled == 0 && s.Status == "open" {
		p.Blockers = append(p.Blockers, "No lots are placed in a container yet.")
	}
	missingCount := 0
	for _, l := range p.All {
		if l.Booking.Mode == "pool" && len(l.MissingDocs) > 0 {
			missingCount++
		}
	}
	if missingCount > 0 {
		p.Warnings = append(p.Warnings, fmt.Sprintf("%d shipper(s) still missing documents.", missingCount))
	}
	if len(p.Held) > 0 {
		p.Warnings = append(p.Warnings, fmt.Sprintf("%d booking(s) on hold will be released when you finalize.", len(p.Held)))
	}
	return p, nil
}

func (a *App) nextSailing(ctx context.Context, s *Sailing) (*Sailing, error) {
	var id int64
	err := a.DB.QueryRow(ctx, `SELECT id FROM sailings WHERE lane_id=$1 AND (year,week) > ($2,$3) AND status='open' ORDER BY year, week LIMIT 1`, s.Lane.ID, s.Year, s.Week).Scan(&id)
	if err != nil {
		return nil, notFound(err)
	}
	return a.SailingByID(ctx, id)
}

func (a *App) pendingMoves(ctx context.Context, sailingID int64) (map[int64]*MoveRequest, error) {
	rows, err := a.DB.Query(ctx, `SELECT id,booking_id,target,COALESCE(to_sailing_id,0),reason,status,created_at FROM move_requests WHERE from_sailing_id=$1 AND status='pending'`, sailingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]*MoveRequest{}
	for rows.Next() {
		m := &MoveRequest{}
		if err := rows.Scan(&m.ID, &m.BookingID, &m.Target, &m.ToSailingID, &m.Reason, &m.Status, &m.CreatedAt); err != nil {
			return nil, err
		}
		out[m.BookingID] = m
	}
	return out, rows.Err()
}

// PendingMoveFor returns the open move request on a booking, if any.
func (a *App) PendingMoveFor(ctx context.Context, bookingID int64) *MoveRequest {
	m := &MoveRequest{}
	err := a.DB.QueryRow(ctx, `SELECT id,booking_id,target,COALESCE(to_sailing_id,0),reason,status,created_at FROM move_requests WHERE booking_id=$1 AND status='pending' ORDER BY id DESC LIMIT 1`, bookingID).
		Scan(&m.ID, &m.BookingID, &m.Target, &m.ToSailingID, &m.Reason, &m.Status, &m.CreatedAt)
	if err != nil {
		return nil
	}
	return m
}

func (a *App) requireOpen(ctx context.Context, q Querier, sailingID int64) (*Sailing, error) {
	if _, err := q.Exec(ctx, `SELECT id FROM sailings WHERE id=$1 FOR UPDATE`, sailingID); err != nil {
		return nil, err
	}
	s, err := a.SailingByID(ctx, sailingID)
	if err != nil {
		return nil, err
	}
	if s.Status != "open" {
		return nil, Errf("The FCL for %s is finalized. Changes aren't possible now.", s.Label())
	}
	return s, nil
}

// AutoPlace places every unplaced pool lot, in booking order, where it fits.
func (a *App) AutoPlace(ctx context.Context, actor *User, sailingID int64) (int, error) {
	placed := 0
	err := a.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := a.requireOpen(ctx, tx, sailingID); err != nil {
			return err
		}
		boxes, cs, err := a.loadBoxes(ctx, tx, sailingID)
		if err != nil {
			return err
		}
		un, err := a.queryBookings(ctx, tx, `b.sailing_id=$1 AND b.status='booked' AND b.mode='pool' AND b.container_id IS NULL ORDER BY b.confirmed_at, b.id`, sailingID)
		if err != nil {
			return err
		}
		for _, b := range un {
			// Try boxes of the booked type first, then any other box.
			order := []int{}
			for i, box := range boxes {
				if box.Spec.Code == b.BoxCode {
					order = append(order, i)
				}
			}
			for i, box := range boxes {
				if box.Spec.Code != b.BoxCode {
					order = append(order, i)
				}
			}
			for _, i := range order {
				if boxes[i].Locked {
					continue
				}
				if boxes[i].Place(domain.Lot{ID: b.ID, CBM: b.CBM, Kg: b.Kg, Category: b.Category}) == nil {
					if _, err := tx.Exec(ctx, `UPDATE bookings SET container_id=$2, box_code=$3 WHERE id=$1`, b.ID, cs[i].ID, cs[i].BoxCode); err != nil {
						return err
					}
					placed++
					break
				}
			}
		}
		return a.Audit(ctx, tx, actor, "plan.autoplace", "sailing", sailingID, fmt.Sprintf("Auto-placed %d lot(s)", placed), nil)
	})
	return placed, err
}

// PlaceLot moves a booked lot into a container (0 to take it out).
func (a *App) PlaceLot(ctx context.Context, actor *User, bookingID, containerID int64) error {
	return a.Tx(ctx, func(tx pgx.Tx) error {
		bs, err := a.queryBookings(ctx, tx, `b.id=$1`, bookingID)
		if err != nil || len(bs) == 0 {
			return ErrNotFound
		}
		b := bs[0]
		if b.Status != "booked" || b.Mode != "pool" {
			return Errf("Only booked pool lots can be placed.")
		}
		s, err := a.requireOpen(ctx, tx, b.SailingID)
		if err != nil {
			return err
		}
		if containerID == 0 {
			if _, err := tx.Exec(ctx, `UPDATE bookings SET container_id=NULL WHERE id=$1`, bookingID); err != nil {
				return err
			}
			return a.Audit(ctx, tx, actor, "plan.unplace", "booking", bookingID, b.Ref+" taken out of its container", nil)
		}
		boxes, cs, err := a.loadBoxes(ctx, tx, s.ID)
		if err != nil {
			return err
		}
		for i, c := range cs {
			if c.ID != containerID {
				continue
			}
			box := boxes[i]
			// Ignore the lot itself if it's already in this box.
			var keep []domain.Lot
			for _, l := range box.Lots {
				if l.ID != bookingID {
					keep = append(keep, l)
				}
			}
			box.Lots = keep
			if err := box.CanPlace(domain.Lot{CBM: b.CBM, Kg: b.Kg, Category: b.Category}); err != nil {
				return Errf("%s can't go in Container %d: %s.", b.Ref, c.Seq, err.Error())
			}
			if _, err := tx.Exec(ctx, `UPDATE bookings SET container_id=$2 WHERE id=$1`, bookingID, containerID); err != nil {
				return err
			}
			return a.Audit(ctx, tx, actor, "plan.place", "booking", bookingID, fmt.Sprintf("%s placed in Container %d", b.Ref, c.Seq), nil)
		}
		return ErrNotFound
	})
}

// AddContainer adds a box to a sailing.
func (a *App) AddContainer(ctx context.Context, actor *User, sailingID int64, boxCode string, grade domain.Grade) error {
	if !grade.Valid() {
		return Errf("Choose a container grade.")
	}
	return a.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := a.requireOpen(ctx, tx, sailingID); err != nil {
			return err
		}
		var seq int
		if err := tx.QueryRow(ctx, `SELECT COALESCE(max(seq),0)+1 FROM containers WHERE sailing_id=$1`, sailingID).Scan(&seq); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO containers(sailing_id,seq,box_code,grade) VALUES ($1,$2,$3,$4)`, sailingID, seq, boxCode, grade); err != nil {
			return err
		}
		return a.Audit(ctx, tx, actor, "plan.container_add", "sailing", sailingID, fmt.Sprintf("Added Container %d · %s grade %s", seq, boxCode, grade), nil)
	})
}

// UpdateContainer changes a box's type or grade. Lots that no longer fit are
// taken out to wait for placement; it returns how many.
func (a *App) UpdateContainer(ctx context.Context, actor *User, containerID int64, boxCode string, grade domain.Grade) (int, error) {
	c, err := a.ContainerByID(ctx, containerID)
	if err != nil {
		return 0, err
	}
	specs, err := a.ContainerSpecs(ctx)
	if err != nil {
		return 0, err
	}
	spec, ok := domain.SpecByCode(specs, boxCode)
	if !ok || !grade.Valid() {
		return 0, Errf("Choose a container type and grade.")
	}
	var removed int
	err = a.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := a.requireOpen(ctx, tx, c.SailingID); err != nil {
			return err
		}
		lots, err := a.queryBookings(ctx, tx, `b.container_id=$1 AND b.status='booked' ORDER BY b.confirmed_at, b.id`, containerID)
		if err != nil {
			return err
		}
		box := domain.NewBox(spec, grade, a.Settings(ctx).Stowage)
		var out []string
		for _, l := range lots {
			if err := box.Place(domain.Lot{ID: l.ID, CBM: l.CBM, Kg: l.Kg, Category: l.Category}); err != nil {
				out = append(out, l.Ref)
				if _, err := tx.Exec(ctx, `UPDATE bookings SET container_id=NULL WHERE id=$1`, l.ID); err != nil {
					return err
				}
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE containers SET box_code=$2, grade=$3 WHERE id=$1`, containerID, boxCode, grade); err != nil {
			return err
		}
		// Bookings follow their box's type so shippers see the right space.
		if _, err := tx.Exec(ctx, `UPDATE bookings SET box_code=$2 WHERE container_id=$1`, containerID, boxCode); err != nil {
			return err
		}
		sum := fmt.Sprintf("Container %d changed to %s grade %s", c.Seq, boxCode, grade)
		if len(out) > 0 {
			sum += "; taken out: " + strings.Join(out, ", ")
		}
		removed = len(out)
		return a.Audit(ctx, tx, actor, "plan.container_update", "sailing", c.SailingID, sum, nil)
	})
	return removed, err
}

// RemoveContainer cancels a box; its lots become unplaced.
func (a *App) RemoveContainer(ctx context.Context, actor *User, containerID int64) error {
	c, err := a.ContainerByID(ctx, containerID)
	if err != nil {
		return err
	}
	return a.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := a.requireOpen(ctx, tx, c.SailingID); err != nil {
			return err
		}
		var n int
		_ = tx.QueryRow(ctx, `SELECT count(*) FROM containers WHERE sailing_id=$1 AND status<>'cancelled'`, c.SailingID).Scan(&n)
		if n <= 1 {
			return Errf("A sailing needs at least one container. Change its type instead.")
		}
		if _, err := tx.Exec(ctx, `UPDATE bookings SET container_id=NULL WHERE container_id=$1`, containerID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE containers SET status='cancelled' WHERE id=$1`, containerID); err != nil {
			return err
		}
		return a.Audit(ctx, tx, actor, "plan.container_remove", "sailing", c.SailingID, fmt.Sprintf("Removed Container %d", c.Seq), nil)
	})
}

// ChooseVessel sets the sailing's vessel; choosing a non-default vessel is logged as an override.
func (a *App) ChooseVessel(ctx context.Context, actor *User, sailingID, vesselID int64) error {
	return a.Tx(ctx, func(tx pgx.Tx) error {
		s, err := a.requireOpen(ctx, tx, sailingID)
		if err != nil {
			return err
		}
		v := s.VesselByID(vesselID)
		if v == nil {
			return ErrNotFound
		}
		if _, err := tx.Exec(ctx, `UPDATE sailings SET chosen_vessel_id=$2 WHERE id=$1`, sailingID, vesselID); err != nil {
			return err
		}
		action := "Vessel set to "
		if !v.IsDefault {
			action = "Vessel overridden to "
		}
		return a.Audit(ctx, tx, actor, "plan.vessel", "sailing", sailingID, action+v.Label()+", "+v.ServiceLabel(), nil)
	})
}

// ---------- Moves ----------

var MoveReasons = []string{
	"Week's container is full",
	"Faster vessel next week",
	"Cargo can't share this container",
	"Container plan changed",
}

type MoveInput struct {
	BookingID  int64
	Target     string // next_week, extra_box, coload
	BoxCode    string // for extra_box
	Reason     string
	AskShipper bool
}

// RequestMove moves a lot, or asks the shipper first when AskShipper is set.
func (a *App) RequestMove(ctx context.Context, actor *User, in MoveInput) error {
	if in.Target != "next_week" && in.Target != "extra_box" && in.Target != "coload" {
		return Errf("Choose where to move the lot.")
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Reason == "" {
		return Errf("Give a reason the shipper will see.")
	}
	bs, err := a.queryBookings(ctx, a.DB, `b.id=$1`, in.BookingID)
	if err != nil || len(bs) == 0 {
		return ErrNotFound
	}
	b := bs[0]
	if b.Status != "booked" || b.Priced() {
		return Errf("Only booked lots that aren't priced yet can be moved.")
	}
	return a.Tx(ctx, func(tx pgx.Tx) error {
		s, err := a.requireOpen(ctx, tx, b.SailingID)
		if err != nil {
			return err
		}
		var toSailing any
		if in.Target == "next_week" {
			next, err := a.nextSailing(ctx, s)
			if err != nil {
				return Errf("There's no open next sailing on %s. Create one in Lanes and sailings first.", s.Lane.Short())
			}
			toSailing = next.ID
		}
		if _, err := tx.Exec(ctx, `UPDATE move_requests SET status='cancelled', resolved_at=now() WHERE booking_id=$1 AND status='pending'`, b.ID); err != nil {
			return err
		}
		if in.AskShipper {
			var mid int64
			if err := tx.QueryRow(ctx, `INSERT INTO move_requests(booking_id,from_sailing_id,target,to_sailing_id,reason,requested_by) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
				b.ID, s.ID, in.Target, toSailing, in.Reason, actor.ID).Scan(&mid); err != nil {
				return err
			}
			m := &MoveRequest{Target: in.Target}
			if err := a.Audit(ctx, tx, actor, "move.request", "booking", b.ID, fmt.Sprintf("Asked %s to move %s (%.1f CBM) to %s", b.CompanyName, b.Ref, b.CBM, m.TargetLabel()), in); err != nil {
				return err
			}
			return a.NotifyCompany(ctx, tx, b.CompanyID, Note{
				Title: "Please confirm a change to " + b.Ref,
				Body:  fmt.Sprintf("Eimsky would like to move your %.1f CBM to %s. Reason: %s. Your cost cap of USD %s stays the same.", b.CBM, m.TargetLabel(), in.Reason, domain.MoneyShort(b.CapCents)),
				Link:  fmt.Sprintf("/f/bookings/%d", b.ID), Email: true,
			})
		}
		if err := a.applyMove(ctx, tx, actor, b, s, in.Target, in.BoxCode); err != nil {
			return err
		}
		m := &MoveRequest{Target: in.Target}
		return a.NotifyCompany(ctx, tx, b.CompanyID, Note{
			Title: "Your booking " + b.Ref + " was moved",
			Body:  fmt.Sprintf("Your %.1f CBM now goes on %s. Reason: %s. Your cost cap of USD %s stays the same.", b.CBM, m.TargetLabel(), in.Reason, domain.MoneyShort(b.CapCents)),
			Link:  fmt.Sprintf("/f/bookings/%d", b.ID), Email: true,
		})
	})
}

func (a *App) applyMove(ctx context.Context, tx pgx.Tx, actor *User, b *Booking, s *Sailing, target, boxCode string) error {
	switch target {
	case "next_week":
		next, err := a.nextSailing(ctx, s)
		if err != nil {
			return Errf("There's no open next sailing on %s.", s.Lane.Short())
		}
		cs, err := a.Containers(ctx, tx, next.ID)
		if err != nil {
			return err
		}
		code := b.BoxCode
		has := false
		for _, c := range cs {
			has = has || c.BoxCode == code
		}
		if !has && len(cs) > 0 {
			code = cs[0].BoxCode
		}
		if _, err := tx.Exec(ctx, `UPDATE bookings SET sailing_id=$2, container_id=NULL, box_code=$3 WHERE id=$1`, b.ID, next.ID, code); err != nil {
			return err
		}
		b.SailingID, b.BoxCode = next.ID, code
		if err := a.tryPlace(ctx, tx, b); err != nil {
			return err
		}
		return a.Audit(ctx, tx, actor, "move.apply", "booking", b.ID, fmt.Sprintf("%s (%.1f CBM) moved to %s", b.Ref, b.CBM, next.Label()), nil)
	case "extra_box":
		if boxCode == "" {
			boxCode = s.Lane.DefaultBox
		}
		var seq int
		if err := tx.QueryRow(ctx, `SELECT COALESCE(max(seq),0)+1 FROM containers WHERE sailing_id=$1`, s.ID).Scan(&seq); err != nil {
			return err
		}
		grade := s.Lane.DefaultGrade
		if domain.GradeAllows(grade, b.Category) != nil {
			grade = domain.GradeB
		}
		var cid int64
		if err := tx.QueryRow(ctx, `INSERT INTO containers(sailing_id,seq,box_code,grade) VALUES ($1,$2,$3,$4) RETURNING id`, s.ID, seq, boxCode, grade).Scan(&cid); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE bookings SET container_id=$2, box_code=$3 WHERE id=$1`, b.ID, cid, boxCode); err != nil {
			return err
		}
		return a.Audit(ctx, tx, actor, "move.apply", "booking", b.ID, fmt.Sprintf("%s placed in new Container %d (%s)", b.Ref, seq, boxCode), nil)
	case "coload":
		if err := a.priceCoload(ctx, tx, b, s); err != nil {
			return err
		}
		return a.Audit(ctx, tx, actor, "move.apply", "booking", b.ID, fmt.Sprintf("%s (%.1f CBM) co-loaded on %s", b.Ref, b.CBM, s.Label()), nil)
	}
	return Errf("Unknown move.")
}

func (a *App) priceCoload(ctx context.Context, q Querier, b *Booking, s *Sailing) error {
	st := a.Settings(ctx)
	bill := domain.CoLoadBill(b.ID, b.WM, s.Lane.ColoadRate, st.FeePerWM, st.HandlingCents, b.CapCents)
	_, err := q.Exec(ctx, `UPDATE bookings SET mode='coload', container_id=NULL, freight_cents=$2, fee_cents=$3, handling_cents=$6, total_cents=$4, cap_applied=$5, priced_at=now() WHERE id=$1`,
		b.ID, bill.FreightCents, bill.FeeCents, bill.TotalCents, bill.CapApplied, bill.HandlingCents)
	return err
}

// RespondMove lets a shipper accept or decline a move. Declining sends the lot to co-load on its sailing.
func (a *App) RespondMove(ctx context.Context, u *User, moveID int64, accept bool) error {
	var bookingID int64
	var target string
	var status string
	if err := a.DB.QueryRow(ctx, `SELECT booking_id,target,status FROM move_requests WHERE id=$1`, moveID).Scan(&bookingID, &target, &status); err != nil {
		return ErrNotFound
	}
	b, err := a.BookingFor(ctx, u, bookingID)
	if err != nil {
		return err
	}
	if status != "pending" {
		return Errf("This request was already answered.")
	}
	return a.Tx(ctx, func(tx pgx.Tx) error {
		s, err := a.requireOpen(ctx, tx, b.SailingID)
		if err != nil {
			return err
		}
		st := "accepted"
		if !accept {
			st = "declined"
			target = "coload"
		}
		if _, err := tx.Exec(ctx, `UPDATE move_requests SET status=$2, resolved_at=now() WHERE id=$1`, moveID, st); err != nil {
			return err
		}
		if err := a.applyMove(ctx, tx, u, b, s, target, ""); err != nil {
			return err
		}
		verb := "accepted"
		if !accept {
			verb = "declined (lot co-loaded)"
		}
		if err := a.Audit(ctx, tx, u, "move."+st, "booking", b.ID, fmt.Sprintf("%s %s the move of %s", b.CompanyName, verb, b.Ref), nil); err != nil {
			return err
		}
		return a.NotifyStaff(ctx, tx, Note{Title: fmt.Sprintf("%s %s a move", b.CompanyName, verb), Body: b.Ref, Link: fmt.Sprintf("/ops/sailings/%d/plan", s.ID)})
	})
}

// ---------- Pools board and decisions ----------

type PoolCard struct {
	Sailing      *Sailing
	BoxCode      string
	Containers   int
	Shippers     int
	Used         float64
	Usable       float64
	Overflow     float64
	BreakEvenPct float64
	Badge        string // DECIDE, FILLING, CO-LOAD, BIDDING, AWARDED, CLOSED
}

func (c PoolCard) FillPct() float64 {
	if c.Usable <= 0 {
		return 0
	}
	return c.Used / c.Usable * 100
}

// PoolsBoard summarises every sailing in a week.
func (a *App) PoolsBoard(ctx context.Context, year, week int) ([]PoolCard, error) {
	ss, err := a.Sailings(ctx, SailingFilter{Year: year, Week: week})
	if err != nil {
		return nil, err
	}
	st := a.Settings(ctx)
	var out []PoolCard
	for _, s := range ss {
		if s.Status == "cancelled" {
			continue
		}
		c, err := a.poolCard(ctx, s, st)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// LanePoolCards summarises a list of sailings (for the planner's sailing strip).
func (a *App) LanePoolCards(ctx context.Context, ss []*Sailing) ([]PoolCard, error) {
	st := a.Settings(ctx)
	var out []PoolCard
	for _, s := range ss {
		if s.Status == "cancelled" {
			continue
		}
		c, err := a.poolCard(ctx, s, st)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func (a *App) poolCard(ctx context.Context, s *Sailing, st Settings) (PoolCard, error) {
	c := PoolCard{Sailing: s}
	cs, err := a.Containers(ctx, a.DB, s.ID)
	if err != nil {
		return c, err
	}
	c.Containers = len(cs)
	for i, x := range cs {
		if i == 0 {
			c.BoxCode = x.BoxCode
		}
		c.Usable += x.Spec.UsableCBM(st.Stowage)
	}
	var placed float64
	_ = a.DB.QueryRow(ctx, `SELECT count(DISTINCT company_id), COALESCE(sum(cbm),0), COALESCE(sum(cbm) FILTER (WHERE container_id IS NOT NULL),0)
		FROM bookings WHERE sailing_id=$1 AND status='booked' AND mode='pool'`, s.ID).Scan(&c.Shippers, &c.Used, &placed)
	c.Overflow = domain.Round(c.Used-c.Usable, 2)
	if c.Overflow < 0 {
		c.Overflow = 0
	}
	be := domain.BreakEvenCBM(s.Lane.Rates[c.BoxCode], s.Lane.ColoadRate)
	if c.Usable > 0 && c.Containers > 0 {
		c.BreakEvenPct = be / (c.Usable / float64(c.Containers)) * 100
	}
	switch s.Status {
	case "tendering":
		c.Badge = "BIDDING"
	case "awarded":
		c.Badge = "AWARDED"
	case "coload":
		c.Badge = "CO-LOAD"
	case "closed":
		c.Badge = "CLOSED"
	default:
		near := s.PlanLockAt.Sub(a.Now()) < 72*time.Hour
		switch {
		case near && c.FillPct() >= st.OwnBoxThreshold:
			c.Badge = "DECIDE"
		case near:
			c.Badge = "CO-LOAD"
		default:
			c.Badge = "FILLING"
		}
	}
	return c, nil
}

// Decide applies ops' choice for a sailing: own box, co-load everything, or hybrid.
func (a *App) Decide(ctx context.Context, actor *User, sailingID int64, option string) error {
	p, err := a.Planner(ctx, sailingID)
	if err != nil {
		return err
	}
	var opt *domain.PoolOption
	for i := range p.PoolOptions {
		if p.PoolOptions[i].Key == option {
			opt = &p.PoolOptions[i]
		}
	}
	if opt == nil {
		return Errf("That option isn't available for this sailing.")
	}
	return a.Tx(ctx, func(tx pgx.Tx) error {
		s, err := a.requireOpen(ctx, tx, sailingID)
		if err != nil {
			return err
		}
		switch option {
		case "own":
			if _, err := tx.Exec(ctx, `UPDATE sailings SET decision='own' WHERE id=$1`, sailingID); err != nil {
				return err
			}
		case "coload":
			bs, err := a.queryBookings(ctx, tx, `b.sailing_id=$1 AND b.status='booked' AND b.mode='pool'`, sailingID)
			if err != nil {
				return err
			}
			for _, b := range bs {
				if err := a.priceCoload(ctx, tx, b, s); err != nil {
					return err
				}
				if err := a.NotifyCompany(ctx, tx, b.CompanyID, Note{Title: "Your cost for " + b.Ref + " is set", Body: fmt.Sprintf("%s %s goes with our co-load partner at the partner rate. Your cost cap still applies.", s.Lane.Short(), s.Label()), Link: fmt.Sprintf("/f/bookings/%d", b.ID), Email: true}); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE containers SET status='cancelled' WHERE sailing_id=$1`, sailingID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE bookings SET status='expired' WHERE sailing_id=$1 AND status='held'`, sailingID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE sailings SET decision='coload', status='coload' WHERE id=$1`, sailingID); err != nil {
				return err
			}
		case "roll":
			next, err := a.nextSailing(ctx, s)
			if err != nil {
				return Errf("There's no open next sailing on %s. Create one in Lanes and sailings first.", s.Lane.Short())
			}
			bs, err := a.queryBookings(ctx, tx, `b.sailing_id=$1 AND b.status='booked' AND b.mode='pool' ORDER BY b.confirmed_at, b.id`, sailingID)
			if err != nil {
				return err
			}
			for _, b := range bs {
				if err := a.applyMove(ctx, tx, actor, b, s, "next_week", ""); err != nil {
					return err
				}
				if err := a.NotifyCompany(ctx, tx, b.CompanyID, Note{Title: "Your booking " + b.Ref + " moved to " + next.Label(),
					Body: fmt.Sprintf("%s %s didn't fill enough for its own container, so your %.1f CBM goes on %s instead. Your cost cap of USD %s stays the same.", s.Lane.Short(), s.Label(), b.CBM, next.Label(), domain.MoneyShort(b.CapCents)),
					Link: fmt.Sprintf("/f/bookings/%d", b.ID), Email: true}); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE bookings SET status='expired' WHERE sailing_id=$1 AND status='held'`, sailingID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE containers SET status='cancelled' WHERE sailing_id=$1`, sailingID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE sailings SET decision='roll', status='closed' WHERE id=$1`, sailingID); err != nil {
				return err
			}
		case "hybrid":
			if _, err := tx.Exec(ctx, `UPDATE containers SET status='cancelled' WHERE sailing_id=$1`, sailingID); err != nil {
				return err
			}
			var seq int
			_ = tx.QueryRow(ctx, `SELECT COALESCE(max(seq),0)+1 FROM containers WHERE sailing_id=$1`, sailingID).Scan(&seq)
			if _, err := tx.Exec(ctx, `INSERT INTO containers(sailing_id,seq,box_code,grade) VALUES ($1,$2,$3,$4)`, sailingID, seq, opt.BoxCode, s.Lane.DefaultGrade); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE bookings SET container_id=NULL, box_code=$2 WHERE sailing_id=$1 AND status IN ('booked','held') AND mode='pool'`, sailingID, opt.BoxCode); err != nil {
				return err
			}
			boxes, cs, err := a.loadBoxes(ctx, tx, sailingID)
			if err != nil {
				return err
			}
			bs, err := a.queryBookings(ctx, tx, `b.sailing_id=$1 AND b.status='booked' AND b.mode='pool' ORDER BY b.confirmed_at, b.id`, sailingID)
			if err != nil {
				return err
			}
			for _, b := range bs {
				if len(boxes) > 0 && boxes[0].Place(domain.Lot{ID: b.ID, CBM: b.CBM, Kg: b.Kg, Category: b.Category}) == nil {
					if _, err := tx.Exec(ctx, `UPDATE bookings SET container_id=$2 WHERE id=$1`, b.ID, cs[0].ID); err != nil {
						return err
					}
					continue
				}
				if err := a.priceCoload(ctx, tx, b, s); err != nil {
					return err
				}
				if err := a.NotifyCompany(ctx, tx, b.CompanyID, Note{Title: "Your cost for " + b.Ref + " is set", Body: "Your lot goes with our co-load partner on the same sailing. Your cost cap still applies.", Link: fmt.Sprintf("/f/bookings/%d", b.ID), Email: true}); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE sailings SET decision='hybrid' WHERE id=$1`, sailingID); err != nil {
				return err
			}
		}
		sum := fmt.Sprintf("Decided: %s (USD %.2f per CBM)", opt.Title, opt.PerCBM)
		if opt.Key == "roll" {
			sum = "Decided: " + opt.Title
		}
		return a.Audit(ctx, tx, actor, "plan.decide", "sailing", sailingID, sum, nil)
	})
}

// LockAndTender locks the plan and opens forwarder bidding for each filled container.
func (a *App) LockAndTender(ctx context.Context, actor *User, sailingID int64) ([]int64, error) {
	p, err := a.Planner(ctx, sailingID)
	if err != nil {
		return nil, err
	}
	if !p.CanLock() {
		return nil, Errf("Not ready to finalize: %s", strings.Join(p.Blockers, " "))
	}
	st := a.Settings(ctx)
	var tenderIDs []int64
	err = a.Tx(ctx, func(tx pgx.Tx) error {
		s, err := a.requireOpen(ctx, tx, sailingID)
		if err != nil {
			return err
		}
		vessel := s.Vessel()
		closes := s.BidCloseAt
		if min := a.Now().Add(time.Duration(st.MinBidHours) * time.Hour); closes.Before(min) {
			closes = min
		}
		// Forwarders serving this lane.
		fw, err := tx.Query(ctx, `SELECT c.id FROM companies c JOIN forwarder_lanes fl ON fl.company_id=c.id WHERE c.kind='forwarder' AND c.status='approved' AND fl.lane_id=$1`, s.Lane.ID)
		if err != nil {
			return err
		}
		var forwarders []int64
		for fw.Next() {
			var id int64
			if err := fw.Scan(&id); err != nil {
				fw.Close()
				return err
			}
			forwarders = append(forwarders, id)
		}
		fw.Close()

		for _, pb := range p.Boxes {
			if len(pb.Lots) == 0 {
				if _, err := tx.Exec(ctx, `UPDATE containers SET status='cancelled' WHERE id=$1`, pb.Container.ID); err != nil {
					return err
				}
				continue
			}
			var seq int64
			if err := tx.QueryRow(ctx, `SELECT nextval('tender_ref_seq')`).Scan(&seq); err != nil {
				return err
			}
			ref := fmt.Sprintf("TND-%02d-%04d", s.Week, seq)
			var tid int64
			if err := tx.QueryRow(ctx, `INSERT INTO tenders(ref,container_id,vessel_id,closes_at) VALUES ($1,$2,$3,$4) RETURNING id`, ref, pb.Container.ID, vessel.ID, closes).Scan(&tid); err != nil {
				return err
			}
			tenderIDs = append(tenderIDs, tid)
			if _, err := tx.Exec(ctx, `UPDATE containers SET status='tendering' WHERE id=$1`, pb.Container.ID); err != nil {
				return err
			}
			for _, f := range forwarders {
				if _, err := tx.Exec(ctx, `INSERT INTO tender_invites(tender_id,company_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, tid, f); err != nil {
					return err
				}
				if err := a.NotifyCompany(ctx, tx, f, Note{
					Title: fmt.Sprintf("New tender: 1 × %s %s", pb.Container.BoxCode, s.Lane.Short()),
					Body:  fmt.Sprintf("%s · %.1f CBM · %.1f t. Bids close %s.", s.Label(), pb.Box.UsedCBM(), pb.Box.UsedKg()/1000, fmtTime(closes)),
					Push:  true, Email: true, Data: map[string]any{"tenderId": tid},
				}); err != nil {
					return err
				}
			}
			for _, l := range pb.Lots {
				if err := a.NotifyCompany(ctx, tx, l.Booking.CompanyID, Note{Title: "Container finalized for " + l.Booking.Ref, Body: fmt.Sprintf("Your %.1f CBM is in a %s on %s. Forwarders are bidding now; your cost won't go above USD %s.", l.Booking.CBM, pb.Container.BoxCode, vessel.Label(), domain.MoneyShort(l.Booking.CapCents)), Link: fmt.Sprintf("/f/bookings/%d", l.Booking.ID)}); err != nil {
					return err
				}
			}
			if err := a.Audit(ctx, tx, actor, "tender.open", "tender", tid, fmt.Sprintf("FCL finalized, tender %s opened to %d forwarders", ref, len(forwarders)), nil); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE bookings SET status='expired' WHERE sailing_id=$1 AND status='held'`, sailingID); err != nil {
			return err
		}
		status := "tendering"
		if len(tenderIDs) == 0 {
			status = "coload"
		}
		if _, err := tx.Exec(ctx, `UPDATE sailings SET status=$2, decision=CASE WHEN decision='' THEN 'own' ELSE decision END WHERE id=$1`, sailingID, status); err != nil {
			return err
		}
		return a.Audit(ctx, tx, actor, "plan.lock", "sailing", sailingID, fmt.Sprintf("FCL finalized for %s %s", s.Lane.Short(), s.Label()), nil)
	})
	return tenderIDs, err
}
