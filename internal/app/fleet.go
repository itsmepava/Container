package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/itsmepava/container/internal/domain"
)

// ---------- Container options per lane (the planner's fleet) ----------

// LaneOptions lists a lane's container options in the order they were added.
func (a *App) LaneOptions(ctx context.Context, laneID int64) ([]domain.FleetOption, error) {
	specs, err := a.ContainerSpecs(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := a.DB.Query(ctx, `SELECT id, box_code, grade, cost FROM lane_options WHERE lane_id=$1 ORDER BY id`, laneID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.FleetOption
	for rows.Next() {
		var o domain.FleetOption
		var code, g string
		if err := rows.Scan(&o.ID, &code, &g, &o.Cost); err != nil {
			return nil, err
		}
		spec, ok := domain.SpecByCode(specs, code)
		if !ok {
			continue // container type switched off in admin
		}
		o.Spec, o.Grade = spec, domain.Grade(g)
		out = append(out, o)
	}
	return out, rows.Err()
}

const maxLaneOptions = 8

// AddLaneOption adds a container type, grade and all-in cost to a lane.
func (a *App) AddLaneOption(ctx context.Context, actor *User, laneID int64, code string, grade domain.Grade, cost float64) error {
	specs, err := a.ContainerSpecs(ctx)
	if err != nil {
		return err
	}
	if _, ok := domain.SpecByCode(specs, code); !ok || !grade.Valid() {
		return Errf("Choose a container type and grade.")
	}
	if cost <= 0 {
		return Errf("Enter the all-in box cost in USD.")
	}
	return a.Tx(ctx, func(tx pgx.Tx) error {
		var n int
		_ = tx.QueryRow(ctx, `SELECT count(*) FROM lane_options WHERE lane_id=$1`, laneID).Scan(&n)
		if n >= maxLaneOptions {
			return Errf("Up to %d container options at a time: remove one first.", maxLaneOptions)
		}
		tag, err := tx.Exec(ctx, `INSERT INTO lane_options(lane_id,box_code,grade,cost) VALUES ($1,$2,$3,$4)
			ON CONFLICT (lane_id,box_code,grade) DO UPDATE SET cost=EXCLUDED.cost`, laneID, code, grade, cost)
		if err != nil {
			return err
		}
		_ = tag
		return a.Audit(ctx, tx, actor, "lane.option_add", "lane", laneID, fmt.Sprintf("Container option %s grade %s at USD %.0f", code, grade, cost), nil)
	})
}

// RemoveLaneOption removes a container option and returns its lane.
func (a *App) RemoveLaneOption(ctx context.Context, actor *User, id int64) (int64, error) {
	var laneID int64
	var code, g string
	err := a.Tx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `DELETE FROM lane_options WHERE id=$1 RETURNING lane_id, box_code, grade`, id).Scan(&laneID, &code, &g); err != nil {
			return notFound(err)
		}
		return a.Audit(ctx, tx, actor, "lane.option_remove", "lane", laneID, fmt.Sprintf("Removed container option %s grade %s", code, g), nil)
	})
	return laneID, err
}

// SavePlannerMoney saves the planner's "Loading and money" panel: the
// stowage factor (all lanes), and the lane's selling price and handling
// cost per W/M. The selling price is also the estimate factories see.
func (a *App) SavePlannerMoney(ctx context.Context, actor *User, laneID int64, stowagePct, sell, handling float64) error {
	if stowagePct < 70 || stowagePct > 95 {
		return Errf("Usable space must be between 70%% and 95%%.")
	}
	if sell <= 0 {
		return Errf("Enter the selling price per W/M.")
	}
	if handling < 0 || handling >= sell {
		return Errf("Handling cost per W/M must be less than the selling price.")
	}
	if err := a.SaveSettings(ctx, actor, map[string]string{"stowage": fmt.Sprintf("%.2f", stowagePct/100)}); err != nil {
		return err
	}
	return a.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE lanes SET estimate_per_wm=$2, handling_per_wm=$3 WHERE id=$1`, laneID, sell, handling); err != nil {
			return err
		}
		return a.Audit(ctx, tx, actor, "lane.money", "lane", laneID, fmt.Sprintf("Selling price USD %.2f/W/M, handling USD %.2f/W/M", sell, handling), nil)
	})
}

// ---------- Bookings and sailings added from the planner ----------

// OpsBookingInput is a booking ops enters for a factory (phone or email order).
type OpsBookingInput struct {
	SailingID int64
	CompanyID int64
	CBM, Kg   float64
	Category  domain.Category
}

// OpsAddBooking books space for a factory directly, without cargo lines.
func (a *App) OpsAddBooking(ctx context.Context, actor *User, in OpsBookingInput) (int64, error) {
	if !actor.IsStaff() {
		return 0, ErrForbidden
	}
	if in.CompanyID == 0 {
		return 0, Errf("Choose the shipper.")
	}
	if !(in.CBM > 0) {
		return 0, Errf("Enter the volume in CBM (more than 0).")
	}
	if !(in.Kg > 0) {
		return 0, Errf("Enter the gross weight in kg.")
	}
	if in.CBM > 70 {
		return 0, Errf("That's more than a 45ft holds: book it as its own FCL.")
	}
	if !in.Category.Valid() {
		return 0, Errf("Choose a cargo category.")
	}
	if in.Category == domain.CatDG {
		return 0, Errf("Dangerous goods aren't pooled in phase 1: book a separate DG-approved box.")
	}
	co, err := a.CompanyByID(ctx, in.CompanyID)
	if err != nil {
		return 0, err
	}
	if co.Kind != "factory" || co.Status != "approved" {
		return 0, Errf("%s isn't an approved factory.", co.Name)
	}
	var id int64
	err = a.Tx(ctx, func(tx pgx.Tx) error {
		s, err := a.requireOpen(ctx, tx, in.SailingID)
		if err != nil {
			return err
		}
		cs, err := a.Containers(ctx, tx, s.ID)
		if err != nil {
			return err
		}
		box := s.Lane.DefaultBox
		if len(cs) > 0 {
			box = cs[0].BoxCode
		}
		var seq int64
		if err := tx.QueryRow(ctx, `SELECT nextval('booking_ref_seq')`).Scan(&seq); err != nil {
			return err
		}
		cbm, kg := domain.Round(in.CBM, 2), domain.Round(in.Kg, 1)
		wm := domain.WM(cbm, kg)
		ref := fmt.Sprintf("EIM-%02d-%04d", s.Week, seq)
		if err := tx.QueryRow(ctx, `INSERT INTO bookings(ref,company_id,created_by,sailing_id,box_code,category,pieces,cbm,kg,wm,cap_cents,status,confirmed_at)
			VALUES ($1,$2,$3,$4,$5,$6,0,$7,$8,$9,$10,'booked',now()) RETURNING id`,
			ref, in.CompanyID, actor.ID, s.ID, box, in.Category, cbm, kg, wm, domain.Cents(s.Lane.EstimatePerWM*wm)).Scan(&id); err != nil {
			return err
		}
		b := &Booking{ID: id, SailingID: s.ID, BoxCode: box, CBM: cbm, Kg: kg, Category: in.Category}
		if err := a.tryPlace(ctx, tx, b); err != nil {
			return err
		}
		if err := a.Audit(ctx, tx, actor, "booking.ops_add", "booking", id, fmt.Sprintf("Booked %s for %s · %.2f CBM", ref, co.Name, cbm), nil); err != nil {
			return err
		}
		return a.NotifyCompany(ctx, tx, in.CompanyID, Note{Title: "Booking added: " + ref,
			Body: fmt.Sprintf("Eimsky booked %.2f CBM for you on %s, %s. Estimated cost up to USD %s. Upload documents before the CFS cut-off, %s.",
				cbm, s.Lane.Short(), s.Label(), domain.MoneyShort(domain.Cents(s.Lane.EstimatePerWM*wm)), fmtTime(s.CFSCutoff)),
			Link: fmt.Sprintf("/f/bookings/%d", id), Email: true})
	})
	return id, err
}

// OpsRemoveBooking cancels a booking from the planner.
func (a *App) OpsRemoveBooking(ctx context.Context, actor *User, id int64) (int64, error) {
	b, err := a.BookingFor(ctx, actor, id)
	if err != nil {
		return 0, err
	}
	return b.SailingID, a.CancelBooking(ctx, actor, id, "Removed by Eimsky ops in the load planner")
}

// QuickSailingInput is the planner's "Add a sailing on this lane" form.
type QuickSailingInput struct {
	LaneID         int64
	Vessel, Voyage string
	ETD            time.Time // date of departure
	CutoffDays     int       // CFS cut-off, days before departure
}

// AddSailingQuick creates a sailing from a vessel, voyage, departure date and
// cut-off. Plan lock and bid close follow the settings; the ETA uses the
// lane's last transit time, or 10 days.
func (a *App) AddSailingQuick(ctx context.Context, actor *User, in QuickSailingInput) (int64, error) {
	in.Vessel, in.Voyage = strings.TrimSpace(in.Vessel), strings.TrimSpace(in.Voyage)
	if in.Vessel == "" {
		return 0, Errf("Enter the vessel name.")
	}
	if in.Voyage == "" {
		return 0, Errf("Enter the voyage number.")
	}
	today := time.Date(a.Now().In(domain.Colombo).Year(), a.Now().In(domain.Colombo).Month(), a.Now().In(domain.Colombo).Day(), 0, 0, 0, 0, domain.Colombo)
	if in.ETD.IsZero() || in.ETD.Before(today) {
		return 0, Errf("Pick a departure date from today onwards.")
	}
	if in.CutoffDays < 1 || in.CutoffDays > 7 {
		return 0, Errf("CFS cut-off must be 1 to 7 days before departure.")
	}
	st := a.Settings(ctx)
	etd := time.Date(in.ETD.Year(), in.ETD.Month(), in.ETD.Day(), 18, 0, 0, 0, domain.Colombo)
	d := etd.AddDate(0, 0, -in.CutoffDays)
	cutoff := time.Date(d.Year(), d.Month(), d.Day(), 17, 0, 0, 0, domain.Colombo)
	if !cutoff.After(a.Now()) {
		return 0, Errf("That cut-off has already passed: pick a later departure.")
	}
	transit := 10
	_ = a.DB.QueryRow(ctx, `SELECT GREATEST(1, EXTRACT(day FROM v.eta - v.etd))::int FROM vessel_options v JOIN sailings s ON s.id=v.sailing_id
		WHERE s.lane_id=$1 ORDER BY v.etd DESC LIMIT 1`, in.LaneID).Scan(&transit)
	y, w := domain.ISOWeek(etd)
	lock := cutoff.Add(-time.Duration(st.DecisionHours) * time.Hour)
	if lock.Before(a.Now()) {
		lock = a.Now().Add(time.Hour)
	}
	closeAt := cutoff.Add(-time.Duration(st.BidCloseHours) * time.Hour)
	if !closeAt.After(lock) {
		closeAt = lock.Add(time.Hour)
	}
	if closeAt.After(cutoff) {
		closeAt = cutoff
	}
	return a.CreateSailing(ctx, actor, SailingInput{LaneID: in.LaneID, Year: y, Week: w, CFSCutoff: cutoff, PlanLockAt: lock, BidCloseAt: closeAt,
		Vessels: []VesselInput{{Name: in.Vessel, Voyage: in.Voyage, Service: "standard", ETD: etd, ETA: etd.AddDate(0, 0, transit), IsDefault: true}}})
}

// ---------- Convert to FCL ----------

// ConvertToFCL books the chosen container option for the sailing: Container 1
// becomes that type and grade and is loaded exactly as the planner showed.
// Lots that don't go in are left unplaced (or go into other containers that
// take them), ready to roll, move or co-load.
func (a *App) ConvertToFCL(ctx context.Context, actor *User, sailingID, optionID int64) (string, error) {
	p, err := a.Planner(ctx, sailingID)
	if err != nil {
		return "", err
	}
	var plan *domain.FleetPlan
	for i := range p.Fleet {
		if p.Fleet[i].Option.ID == optionID {
			plan = &p.Fleet[i]
		}
	}
	if plan == nil {
		return "", Errf("That container option is no longer on this lane.")
	}
	if len(plan.Fit) == 0 {
		return "", Errf("No lot on this sailing can go in a %s grade %s.", plan.Option.Spec.Code, plan.Option.Grade)
	}
	o := plan.Option
	err = a.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := a.requireOpen(ctx, tx, sailingID); err != nil {
			return err
		}
		cs, err := a.Containers(ctx, tx, sailingID)
		if err != nil {
			return err
		}
		var c1 int64
		if len(cs) == 0 {
			if err := tx.QueryRow(ctx, `INSERT INTO containers(sailing_id,seq,box_code,grade) VALUES ($1,1,$2,$3) RETURNING id`, sailingID, o.Spec.Code, o.Grade).Scan(&c1); err != nil {
				return err
			}
		} else {
			c1 = cs[0].ID
			if _, err := tx.Exec(ctx, `UPDATE containers SET box_code=$2, grade=$3 WHERE id=$1`, c1, o.Spec.Code, o.Grade); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE bookings SET container_id=NULL WHERE sailing_id=$1 AND status='booked' AND mode='pool'`, sailingID); err != nil {
			return err
		}
		ids := make([]int64, 0, len(plan.Fit))
		for _, l := range plan.Fit {
			ids = append(ids, l.ID)
		}
		if _, err := tx.Exec(ctx, `UPDATE bookings SET container_id=$2, box_code=$3 WHERE id = ANY($1)`, ids, c1, o.Spec.Code); err != nil {
			return err
		}
		return a.Audit(ctx, tx, actor, "plan.convert", "sailing", sailingID,
			fmt.Sprintf("Converted to FCL: %s grade %s, %d lot(s), %.1f CBM, margin USD %.0f", o.Spec.Code, o.Grade, len(plan.Fit), plan.CBM, plan.Margin), nil)
	})
	if err != nil {
		return "", err
	}
	if len(plan.Rej) > 0 && len(p.Boxes) > 1 {
		_, _ = a.AutoPlace(ctx, actor, sailingID) // other containers may take the rest
	}
	msg := fmt.Sprintf("Converted to FCL: Container 1 is a %s grade %s with %d lot(s), %.1f CBM.", o.Spec.Code, o.Grade, len(plan.Fit), plan.CBM)
	if n := len(plan.Rej); n > 0 {
		msg += fmt.Sprintf(" %d lot(s) wait: roll, move or co-load them.", n)
	}
	return msg, nil
}

// Roll asks the shipper to move a lot to the next sailing.
func (a *App) Roll(ctx context.Context, actor *User, bookingID int64) error {
	return a.RequestMove(ctx, actor, MoveInput{BookingID: bookingID, Target: "next_week", Reason: "Week's container is full", AskShipper: true})
}
