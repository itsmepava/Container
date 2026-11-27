package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/itsmepava/container/internal/domain"
	"github.com/jackc/pgx/v5"
)

type Port struct{ Code, Name, Country string }

func (p Port) Label() string { return fmt.Sprintf("%s (%s)", p.Name, p.Code) }

func (a *App) Ports(ctx context.Context) ([]Port, error) {
	rows, err := a.DB.Query(ctx, `SELECT code,name,country FROM ports ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Port
	for rows.Next() {
		var p Port
		if err := rows.Scan(&p.Code, &p.Name, &p.Country); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (a *App) SavePort(ctx context.Context, actor *User, p Port) error {
	p.Code = strings.ToUpper(strings.TrimSpace(p.Code))
	p.Name = strings.TrimSpace(p.Name)
	if len(p.Code) != 5 || p.Name == "" {
		return Errf("A port needs a 5-letter UN/LOCODE (like CNSHA) and a name.")
	}
	_, err := a.DB.Exec(ctx, `INSERT INTO ports(code,name,country) VALUES ($1,$2,$3) ON CONFLICT (code) DO UPDATE SET name=EXCLUDED.name, country=EXCLUDED.country`, p.Code, p.Name, strings.TrimSpace(p.Country))
	if err == nil {
		_ = a.Audit(ctx, a.DB, actor, "port.save", "port", 0, "Saved port "+p.Code, nil)
	}
	return err
}

// ContainerSpecs returns the active container types.
func (a *App) ContainerSpecs(ctx context.Context) ([]domain.ContainerSpec, error) {
	rows, err := a.DB.Query(ctx, `SELECT code,name,internal_cbm,payload_kg FROM container_types WHERE active ORDER BY sort, code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ContainerSpec
	for rows.Next() {
		var s domain.ContainerSpec
		if err := rows.Scan(&s.Code, &s.Name, &s.InternalCBM, &s.PayloadKg); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (a *App) SaveContainerType(ctx context.Context, actor *User, s domain.ContainerSpec, active bool) error {
	s.Code = strings.ToUpper(strings.TrimSpace(s.Code))
	if s.Code == "" || s.InternalCBM <= 0 || s.PayloadKg <= 0 {
		return Errf("A container type needs a code, internal volume and payload.")
	}
	_, err := a.DB.Exec(ctx, `INSERT INTO container_types(code,name,internal_cbm,payload_kg,active,sort) VALUES ($1,$2,$3,$4,$5,(SELECT COALESCE(max(sort),0)+1 FROM container_types))
		ON CONFLICT (code) DO UPDATE SET name=EXCLUDED.name, internal_cbm=EXCLUDED.internal_cbm, payload_kg=EXCLUDED.payload_kg, active=EXCLUDED.active`,
		s.Code, strings.TrimSpace(s.Name), s.InternalCBM, s.PayloadKg, active)
	if err == nil {
		_ = a.Audit(ctx, a.DB, actor, "container_type.save", "container_type", 0, "Saved container type "+s.Code, s)
	}
	return err
}

// ---------- Lanes ----------

type Lane struct {
	ID            int64
	Origin        Port
	Dest          Port
	Active        bool
	ColoadRate    float64
	EstimatePerWM float64
	OriginCFS     string
	DefaultBox    string
	DefaultGrade  domain.Grade
	Rates         map[string]float64 // expected bid per box code
}

func (l *Lane) Label() string { return l.Origin.Label() + " → " + l.Dest.Label() }
func (l *Lane) Short() string { return l.Origin.Name + " → " + l.Dest.Name }

const laneCols = `l.id, o.code, o.name, o.country, d.code, d.name, d.country, l.active, l.coload_rate, l.estimate_per_wm, l.origin_cfs, l.default_box, l.default_grade`

func scanLane(row pgx.Row) (*Lane, error) {
	l := &Lane{Rates: map[string]float64{}}
	var g string
	err := row.Scan(&l.ID, &l.Origin.Code, &l.Origin.Name, &l.Origin.Country, &l.Dest.Code, &l.Dest.Name, &l.Dest.Country,
		&l.Active, &l.ColoadRate, &l.EstimatePerWM, &l.OriginCFS, &l.DefaultBox, &g)
	l.DefaultGrade = domain.Grade(g)
	return l, err
}

func (a *App) loadRates(ctx context.Context, lanes []*Lane) error {
	if len(lanes) == 0 {
		return nil
	}
	byID := map[int64]*Lane{}
	ids := []int64{}
	for _, l := range lanes {
		byID[l.ID] = l
		ids = append(ids, l.ID)
	}
	rows, err := a.DB.Query(ctx, `SELECT lane_id, box_code, expected_bid FROM lane_box_rates WHERE lane_id = ANY($1)`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var code string
		var v float64
		if err := rows.Scan(&id, &code, &v); err != nil {
			return err
		}
		byID[id].Rates[code] = v
	}
	return rows.Err()
}

func (a *App) Lanes(ctx context.Context, activeOnly bool) ([]*Lane, error) {
	rows, err := a.DB.Query(ctx, `SELECT `+laneCols+` FROM lanes l JOIN ports o ON o.code=l.origin JOIN ports d ON d.code=l.dest
		WHERE ($1=false OR l.active) ORDER BY o.name, d.name`, activeOnly)
	if err != nil {
		return nil, err
	}
	var out []*Lane
	for rows.Next() {
		l, err := scanLane(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, l)
	}
	rows.Close()
	return out, a.loadRates(ctx, out)
}

func (a *App) LaneByID(ctx context.Context, id int64) (*Lane, error) {
	l, err := scanLane(a.DB.QueryRow(ctx, `SELECT `+laneCols+` FROM lanes l JOIN ports o ON o.code=l.origin JOIN ports d ON d.code=l.dest WHERE l.id=$1`, id))
	if err != nil {
		return nil, notFound(err)
	}
	return l, a.loadRates(ctx, []*Lane{l})
}

type LaneInput struct {
	ID            int64
	Origin, Dest  string
	Active        bool
	ColoadRate    float64
	EstimatePerWM float64
	OriginCFS     string
	DefaultBox    string
	DefaultGrade  string
	Rates         map[string]float64
}

func (a *App) SaveLane(ctx context.Context, actor *User, in LaneInput) (int64, error) {
	if in.Origin == "" || in.Dest == "" || in.Origin == in.Dest {
		return 0, Errf("Choose two different ports for the lane.")
	}
	if in.ColoadRate <= 0 || in.EstimatePerWM <= 0 {
		return 0, Errf("Enter the co-load rate and the booking estimate per W/M.")
	}
	if !domain.Grade(in.DefaultGrade).Valid() {
		return 0, Errf("Choose a default container grade.")
	}
	id := in.ID
	err := a.Tx(ctx, func(tx pgx.Tx) error {
		if id == 0 {
			err := tx.QueryRow(ctx, `INSERT INTO lanes(origin,dest,active,coload_rate,estimate_per_wm,origin_cfs,default_box,default_grade) VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
				in.Origin, in.Dest, in.Active, in.ColoadRate, in.EstimatePerWM, strings.TrimSpace(in.OriginCFS), in.DefaultBox, in.DefaultGrade).Scan(&id)
			if err != nil {
				if strings.Contains(err.Error(), "lanes_origin_dest_key") {
					return Errf("That lane already exists.")
				}
				return err
			}
		} else {
			if _, err := tx.Exec(ctx, `UPDATE lanes SET origin=$2,dest=$3,active=$4,coload_rate=$5,estimate_per_wm=$6,origin_cfs=$7,default_box=$8,default_grade=$9 WHERE id=$1`,
				id, in.Origin, in.Dest, in.Active, in.ColoadRate, in.EstimatePerWM, strings.TrimSpace(in.OriginCFS), in.DefaultBox, in.DefaultGrade); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM lane_box_rates WHERE lane_id=$1`, id); err != nil {
			return err
		}
		for code, v := range in.Rates {
			if v > 0 {
				if _, err := tx.Exec(ctx, `INSERT INTO lane_box_rates(lane_id,box_code,expected_bid) VALUES ($1,$2,$3)`, id, code, v); err != nil {
					return err
				}
			}
		}
		return a.Audit(ctx, tx, actor, "lane.save", "lane", id, fmt.Sprintf("Saved lane %s → %s", in.Origin, in.Dest), in)
	})
	return id, err
}

// ---------- Sailings ----------

type VesselOption struct {
	ID          int64
	SailingID   int64
	Name        string
	Voyage      string
	Service     string
	ETD         time.Time
	ETA         time.Time
	Via         string
	ExtraPerBox float64
	IsDefault   bool
}

func (v *VesselOption) Label() string {
	if v == nil {
		return "Vessel to be chosen"
	}
	if v.Voyage == "" {
		return v.Name
	}
	return v.Name + " · " + v.Voyage
}

func (v *VesselOption) TransitDays() int {
	return int(v.ETA.Sub(v.ETD).Hours()/24 + 0.5)
}

func (v *VesselOption) ServiceLabel() string {
	if v.Service == "fast" {
		if v.Via == "" {
			return "fast, direct"
		}
		return "fast"
	}
	return "standard"
}

type Sailing struct {
	ID             int64
	Lane           *Lane
	Year, Week     int
	CFSCutoff      time.Time
	PlanLockAt     time.Time
	BidCloseAt     time.Time
	Status         string
	Decision       string
	ChosenVesselID int64
	Vessels        []*VesselOption
}

func (s *Sailing) Label() string { return fmt.Sprintf("Week %d", s.Week) }

func (s *Sailing) Vessel() *VesselOption {
	for _, v := range s.Vessels {
		if v.ID == s.ChosenVesselID {
			return v
		}
	}
	for _, v := range s.Vessels {
		if v.IsDefault {
			return v
		}
	}
	if len(s.Vessels) > 0 {
		return s.Vessels[0]
	}
	return nil
}

func (s *Sailing) VesselByID(id int64) *VesselOption {
	for _, v := range s.Vessels {
		if v.ID == id {
			return v
		}
	}
	return nil
}

// Bookable reports whether shippers may still book this sailing.
func (s *Sailing) Bookable(now time.Time) bool {
	return s.Status == "open" && now.Before(s.PlanLockAt) && now.Before(s.CFSCutoff)
}

func (s *Sailing) StatusLabel() string {
	switch s.Status {
	case "open":
		return "Open for booking"
	case "tendering":
		return "Forwarders bidding"
	case "awarded":
		return "Awarded"
	case "coload":
		return "Co-loaded"
	case "closed":
		return "Closed"
	case "cancelled":
		return "Cancelled"
	}
	return s.Status
}

const sailingCols = `s.id, s.lane_id, s.year, s.week, s.cfs_cutoff, s.plan_lock_at, s.bid_close_at, s.status, s.decision, COALESCE(s.chosen_vessel_id,0)`

type SailingFilter struct {
	LaneID   int64
	Year     int
	Week     int
	Statuses []string
	FromWeek time.Time // sailings whose cut-off is after this
	Limit    int
}

func (a *App) Sailings(ctx context.Context, f SailingFilter) ([]*Sailing, error) {
	sql := `SELECT ` + sailingCols + ` FROM sailings s WHERE true`
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		sql += fmt.Sprintf(" AND "+cond, len(args))
	}
	if f.LaneID != 0 {
		add("s.lane_id=$%d", f.LaneID)
	}
	if f.Year != 0 {
		add("s.year=$%d", f.Year)
	}
	if f.Week != 0 {
		add("s.week=$%d", f.Week)
	}
	if len(f.Statuses) > 0 {
		add("s.status = ANY($%d)", f.Statuses)
	}
	if !f.FromWeek.IsZero() {
		add("s.cfs_cutoff >= $%d", f.FromWeek)
	}
	sql += " ORDER BY s.year, s.week, s.lane_id"
	if f.Limit > 0 {
		sql += fmt.Sprintf(" LIMIT %d", f.Limit)
	}
	rows, err := a.DB.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	var out []*Sailing
	laneIDs := map[int64]bool{}
	for rows.Next() {
		s := &Sailing{Lane: &Lane{}}
		if err := rows.Scan(&s.ID, &s.Lane.ID, &s.Year, &s.Week, &s.CFSCutoff, &s.PlanLockAt, &s.BidCloseAt, &s.Status, &s.Decision, &s.ChosenVesselID); err != nil {
			rows.Close()
			return nil, err
		}
		laneIDs[s.Lane.ID] = true
		out = append(out, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	lanes := map[int64]*Lane{}
	for id := range laneIDs {
		l, err := a.LaneByID(ctx, id)
		if err != nil {
			return nil, err
		}
		lanes[id] = l
	}
	for _, s := range out {
		s.Lane = lanes[s.Lane.ID]
		if s.Vessels, err = a.vessels(ctx, s.ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (a *App) vessels(ctx context.Context, sailingID int64) ([]*VesselOption, error) {
	rows, err := a.DB.Query(ctx, `SELECT id,sailing_id,name,voyage,service,etd,eta,via,extra_per_box,is_default FROM vessel_options WHERE sailing_id=$1 ORDER BY etd, id`, sailingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*VesselOption
	for rows.Next() {
		v := &VesselOption{}
		if err := rows.Scan(&v.ID, &v.SailingID, &v.Name, &v.Voyage, &v.Service, &v.ETD, &v.ETA, &v.Via, &v.ExtraPerBox, &v.IsDefault); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (a *App) SailingByID(ctx context.Context, id int64) (*Sailing, error) {
	s := &Sailing{Lane: &Lane{}}
	err := a.DB.QueryRow(ctx, `SELECT `+sailingCols+` FROM sailings s WHERE s.id=$1`, id).Scan(&s.ID, &s.Lane.ID, &s.Year, &s.Week, &s.CFSCutoff, &s.PlanLockAt, &s.BidCloseAt, &s.Status, &s.Decision, &s.ChosenVesselID)
	if err != nil {
		return nil, notFound(err)
	}
	if s.Lane, err = a.LaneByID(ctx, s.Lane.ID); err != nil {
		return nil, err
	}
	s.Vessels, err = a.vessels(ctx, s.ID)
	return s, err
}

// BookableSailings lists sailings on a lane that shippers can still book.
func (a *App) BookableSailings(ctx context.Context, laneID int64) ([]*Sailing, error) {
	all, err := a.Sailings(ctx, SailingFilter{LaneID: laneID, Statuses: []string{"open"}, FromWeek: a.Now()})
	if err != nil {
		return nil, err
	}
	var out []*Sailing
	for _, s := range all {
		if s.Bookable(a.Now()) {
			out = append(out, s)
		}
	}
	return out, nil
}

type VesselInput struct {
	Name, Voyage, Service, Via string
	ETD, ETA                   time.Time
	ExtraPerBox                float64
	IsDefault                  bool
}

type SailingInput struct {
	LaneID     int64
	Year, Week int
	CFSCutoff  time.Time
	PlanLockAt time.Time
	BidCloseAt time.Time
	Vessels    []VesselInput
}

func (in SailingInput) validate() error {
	if in.LaneID == 0 || in.Week < 1 || in.Week > 53 || in.Year < 2020 {
		return Errf("Choose a lane and a sailing week.")
	}
	if in.CFSCutoff.IsZero() || in.PlanLockAt.IsZero() || in.BidCloseAt.IsZero() {
		return Errf("Enter the plan lock, bid close and CFS cut-off times.")
	}
	if !in.PlanLockAt.Before(in.BidCloseAt) || in.BidCloseAt.After(in.CFSCutoff) {
		return Errf("Times must run in order: plan lock, then bids close, then CFS cut-off.")
	}
	if len(in.Vessels) == 0 {
		return Errf("Add at least one vessel option.")
	}
	for _, v := range in.Vessels {
		if strings.TrimSpace(v.Name) == "" || v.ETD.IsZero() || v.ETA.IsZero() || !v.ETA.After(v.ETD) {
			return Errf("Each vessel needs a name, ETD and a later ETA.")
		}
		if v.ETD.Before(in.CFSCutoff) {
			return Errf("Vessel %s leaves before the CFS cut-off.", v.Name)
		}
	}
	return nil
}

// CreateSailing creates a sailing week, its vessel options and a first container.
func (a *App) CreateSailing(ctx context.Context, actor *User, in SailingInput) (int64, error) {
	if err := in.validate(); err != nil {
		return 0, err
	}
	lane, err := a.LaneByID(ctx, in.LaneID)
	if err != nil {
		return 0, err
	}
	var id int64
	err = a.Tx(ctx, func(tx pgx.Tx) error {
		var exists bool
		_ = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sailings WHERE lane_id=$1 AND year=$2 AND week=$3)`, in.LaneID, in.Year, in.Week).Scan(&exists)
		if exists {
			return Errf("%s already has a sailing in week %d.", lane.Short(), in.Week)
		}
		if err := tx.QueryRow(ctx, `INSERT INTO sailings(lane_id,year,week,cfs_cutoff,plan_lock_at,bid_close_at) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
			in.LaneID, in.Year, in.Week, in.CFSCutoff, in.PlanLockAt, in.BidCloseAt).Scan(&id); err != nil {
			return err
		}
		hasDefault := false
		for _, v := range in.Vessels {
			hasDefault = hasDefault || v.IsDefault
		}
		var chosen int64
		for i, v := range in.Vessels {
			def := v.IsDefault || (!hasDefault && i == 0)
			svc := v.Service
			if svc != "fast" {
				svc = "standard"
			}
			var vid int64
			if err := tx.QueryRow(ctx, `INSERT INTO vessel_options(sailing_id,name,voyage,service,etd,eta,via,extra_per_box,is_default) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
				id, strings.TrimSpace(v.Name), strings.TrimSpace(v.Voyage), svc, v.ETD, v.ETA, strings.TrimSpace(v.Via), v.ExtraPerBox, def).Scan(&vid); err != nil {
				return err
			}
			if def && chosen == 0 {
				chosen = vid
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE sailings SET chosen_vessel_id=$2 WHERE id=$1`, id, chosen); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO containers(sailing_id,seq,box_code,grade) VALUES ($1,1,$2,$3)`, id, lane.DefaultBox, lane.DefaultGrade); err != nil {
			return err
		}
		return a.Audit(ctx, tx, actor, "sailing.create", "sailing", id, fmt.Sprintf("Created %s week %d", lane.Short(), in.Week), nil)
	})
	return id, err
}

// CopySailingToNextWeek clones a sailing's schedule one week later.
func (a *App) CopySailingToNextWeek(ctx context.Context, actor *User, id int64) (int64, error) {
	s, err := a.SailingByID(ctx, id)
	if err != nil {
		return 0, err
	}
	next := s.CFSCutoff.AddDate(0, 0, 7)
	y, w := domain.ISOWeek(domain.WeekStart(s.Year, s.Week).AddDate(0, 0, 7))
	in := SailingInput{LaneID: s.Lane.ID, Year: y, Week: w, CFSCutoff: next, PlanLockAt: s.PlanLockAt.AddDate(0, 0, 7), BidCloseAt: s.BidCloseAt.AddDate(0, 0, 7)}
	for _, v := range s.Vessels {
		in.Vessels = append(in.Vessels, VesselInput{Name: v.Name, Voyage: nextVoyage(v.Voyage), Service: v.Service, Via: v.Via,
			ETD: v.ETD.AddDate(0, 0, 7), ETA: v.ETA.AddDate(0, 0, 7), ExtraPerBox: v.ExtraPerBox, IsDefault: v.IsDefault})
	}
	return a.CreateSailing(ctx, actor, in)
}

// nextVoyage bumps the number in a voyage code: 049W → 050W.
func nextVoyage(v string) string {
	start, end := -1, -1
	for i, r := range v {
		if r >= '0' && r <= '9' {
			if start < 0 {
				start = i
			}
			end = i + 1
		} else if start >= 0 {
			break
		}
	}
	if start < 0 {
		return v
	}
	var n int
	fmt.Sscanf(v[start:end], "%d", &n)
	return v[:start] + fmt.Sprintf("%0*d", end-start, n+1) + v[end:]
}

func (a *App) AddVessel(ctx context.Context, actor *User, sailingID int64, v VesselInput) error {
	s, err := a.SailingByID(ctx, sailingID)
	if err != nil {
		return err
	}
	if s.Status != "open" {
		return Errf("Vessels can only be added before the plan is locked.")
	}
	if strings.TrimSpace(v.Name) == "" || !v.ETA.After(v.ETD) {
		return Errf("A vessel needs a name, ETD and a later ETA.")
	}
	svc := v.Service
	if svc != "fast" {
		svc = "standard"
	}
	_, err = a.DB.Exec(ctx, `INSERT INTO vessel_options(sailing_id,name,voyage,service,etd,eta,via,extra_per_box) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		sailingID, strings.TrimSpace(v.Name), strings.TrimSpace(v.Voyage), svc, v.ETD, v.ETA, strings.TrimSpace(v.Via), v.ExtraPerBox)
	if err == nil {
		_ = a.Audit(ctx, a.DB, actor, "sailing.vessel_add", "sailing", sailingID, "Added vessel "+v.Name, nil)
	}
	return err
}

// UpdateSailingTimes changes a sailing's deadlines.
func (a *App) UpdateSailingTimes(ctx context.Context, actor *User, id int64, lock, bidClose, cutoff time.Time) error {
	if !lock.Before(bidClose) || bidClose.After(cutoff) {
		return Errf("Times must run in order: plan lock, then bids close, then CFS cut-off.")
	}
	_, err := a.DB.Exec(ctx, `UPDATE sailings SET plan_lock_at=$2, bid_close_at=$3, cfs_cutoff=$4 WHERE id=$1`, id, lock, bidClose, cutoff)
	if err == nil {
		_ = a.Audit(ctx, a.DB, actor, "sailing.times", "sailing", id, "Changed sailing deadlines", nil)
	}
	return err
}

// ---------- Containers ----------

type Container struct {
	ID        int64
	SailingID int64
	Seq       int
	BoxCode   string
	Grade     domain.Grade
	Status    string
	Spec      domain.ContainerSpec
}

func (c *Container) Label() string {
	return fmt.Sprintf("Container %d · %s · grade %s", c.Seq, c.BoxCode, c.Grade)
}

func (a *App) Containers(ctx context.Context, q Querier, sailingID int64) ([]*Container, error) {
	rows, err := q.Query(ctx, `SELECT c.id,c.sailing_id,c.seq,c.box_code,c.grade,c.status,t.name,t.internal_cbm,t.payload_kg
		FROM containers c JOIN container_types t ON t.code=c.box_code WHERE c.sailing_id=$1 AND c.status<>'cancelled' ORDER BY c.seq`, sailingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Container
	for rows.Next() {
		c := &Container{}
		var g string
		if err := rows.Scan(&c.ID, &c.SailingID, &c.Seq, &c.BoxCode, &g, &c.Status, &c.Spec.Name, &c.Spec.InternalCBM, &c.Spec.PayloadKg); err != nil {
			return nil, err
		}
		c.Grade = domain.Grade(g)
		c.Spec.Code = c.BoxCode
		out = append(out, c)
	}
	return out, rows.Err()
}

func (a *App) ContainerByID(ctx context.Context, id int64) (*Container, error) {
	c := &Container{}
	var g string
	err := a.DB.QueryRow(ctx, `SELECT c.id,c.sailing_id,c.seq,c.box_code,c.grade,c.status,t.name,t.internal_cbm,t.payload_kg
		FROM containers c JOIN container_types t ON t.code=c.box_code WHERE c.id=$1`, id).Scan(&c.ID, &c.SailingID, &c.Seq, &c.BoxCode, &g, &c.Status, &c.Spec.Name, &c.Spec.InternalCBM, &c.Spec.PayloadKg)
	c.Grade = domain.Grade(g)
	c.Spec.Code = c.BoxCode
	return c, notFound(err)
}

// HSCode is a tariff code with a description for search.
type HSCode struct{ Code, Description string }

func (a *App) SearchHS(ctx context.Context, q string, limit int) ([]HSCode, error) {
	q = strings.TrimSpace(q)
	rows, err := a.DB.Query(ctx, `SELECT code, description FROM hs_codes
		WHERE $1='' OR code LIKE $2 OR replace(code,'.','') LIKE $2 OR description ILIKE $3
		ORDER BY (code LIKE $2) DESC, code LIMIT $4`, q, strings.ReplaceAll(q, " ", "")+"%", "%"+q+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HSCode
	for rows.Next() {
		var h HSCode
		if err := rows.Scan(&h.Code, &h.Description); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (a *App) HSDescriptions(ctx context.Context, codes []string) map[string]string {
	out := map[string]string{}
	rows, err := a.DB.Query(ctx, `SELECT code, description FROM hs_codes WHERE code = ANY($1)`, codes)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var c, d string
		if rows.Scan(&c, &d) == nil {
			out[c] = d
		}
	}
	return out
}

func sortedKeys(m map[string]float64) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
