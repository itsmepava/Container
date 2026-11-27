package app

import (
	"context"
	"sort"
	"time"

	"github.com/itsmepava/container/internal/domain"
)

// Insights are anonymised totals for JAAF and FAAMA. Any figure built from
// fewer than K factories is hidden.
type Insights struct {
	FromYear, FromWeek int
	ToYear, ToWeek     int
	Weeks              int
	K                  int
	Factories          int
	Suppressed         bool // the whole period has too few factories

	InboundCBM  float64
	Sailings    int
	Containers  int
	AvgFillPct  float64
	AllInPerCBM float64
	ColoadRate  float64
	SavingPct   float64
	FabricPct   float64

	Mix    []Share
	ByLane []Row
	ByWeek []Row
	TopHS  []Row
}

type Share struct {
	Label string
	CBM   float64
	Pct   float64
}

type Row struct {
	Label     string
	CBM       float64
	Factories int
	Hidden    bool
	PerCBM    float64
}

// Insights covers the last `weeks` sailing weeks up to and including the current one.
func (a *App) Insights(ctx context.Context, weeks int) (*Insights, error) {
	if weeks <= 0 {
		weeks = 8
	}
	st := a.Settings(ctx)
	now := a.Now()
	ty, tw := domain.ISOWeek(now.AddDate(0, 0, 14))
	fy, fw := domain.ISOWeek(domain.WeekStart(ty, tw).AddDate(0, 0, -7*(weeks-1)))
	in := &Insights{FromYear: fy, FromWeek: fw, ToYear: ty, ToWeek: tw, Weeks: weeks, K: st.KAnon}
	from := domain.WeekStart(fy, fw)
	to := domain.WeekStart(ty, tw).AddDate(0, 0, 7)
	_ = time.Second

	base := `FROM bookings b JOIN sailings s ON s.id=b.sailing_id JOIN lanes l ON l.id=s.lane_id
		WHERE b.status='booked' AND s.cfs_cutoff >= $1 AND s.cfs_cutoff < $2`
	if err := a.DB.QueryRow(ctx, `SELECT count(DISTINCT b.company_id), COALESCE(sum(b.cbm),0), count(DISTINCT s.id) `+base, from, to).
		Scan(&in.Factories, &in.InboundCBM, &in.Sailings); err != nil {
		return nil, err
	}
	if in.Factories < in.K {
		in.Suppressed = true
		return in, nil
	}
	// Fill of containers that went to tender.
	rows, err := a.DB.Query(ctx, `SELECT c.id, t.internal_cbm, COALESCE((SELECT sum(cbm) FROM bookings WHERE container_id=c.id AND status='booked'),0)
		FROM containers c JOIN container_types t ON t.code=c.box_code JOIN sailings s ON s.id=c.sailing_id
		WHERE c.status IN ('tendering','awarded') AND s.cfs_cutoff >= $1 AND s.cfs_cutoff < $2`, from, to)
	if err != nil {
		return nil, err
	}
	var fillSum float64
	for rows.Next() {
		var id int64
		var internal, used float64
		if err := rows.Scan(&id, &internal, &used); err != nil {
			rows.Close()
			return nil, err
		}
		usable := domain.Round(internal*st.Stowage, 1)
		if usable > 0 {
			fillSum += used / usable * 100
			in.Containers++
		}
	}
	rows.Close()
	if in.Containers > 0 {
		in.AvgFillPct = fillSum / float64(in.Containers)
	}
	var priced, wm, coloadW, coloadSum float64
	_ = a.DB.QueryRow(ctx, `SELECT COALESCE(sum(b.total_cents),0)/100.0, COALESCE(sum(b.wm),0), COALESCE(sum(b.wm*l.coload_rate),0), COALESCE(sum(b.wm),0) `+base+` AND b.priced_at IS NOT NULL AND b.mode='pool'`, from, to).
		Scan(&priced, &wm, &coloadSum, &coloadW)
	if wm > 0 {
		in.AllInPerCBM = priced / wm
		in.ColoadRate = coloadSum / coloadW
		if in.ColoadRate > 0 {
			in.SavingPct = (in.ColoadRate - in.AllInPerCBM) / in.ColoadRate * 100
		}
	}
	// Cargo mix.
	rows, err = a.DB.Query(ctx, `SELECT b.category, sum(b.cbm) `+base+` GROUP BY b.category`, from, to)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c string
		var v float64
		_ = rows.Scan(&c, &v)
		in.Mix = append(in.Mix, Share{Label: domain.Category(c).Label(), CBM: v, Pct: v / in.InboundCBM * 100})
		if c == string(domain.CatFabric) {
			in.FabricPct = v / in.InboundCBM * 100
		}
	}
	rows.Close()
	sort.Slice(in.Mix, func(i, j int) bool { return in.Mix[i].CBM > in.Mix[j].CBM })

	group := func(sql string) ([]Row, error) {
		rows, err := a.DB.Query(ctx, sql, from, to)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []Row
		for rows.Next() {
			var r Row
			if err := rows.Scan(&r.Label, &r.CBM, &r.Factories); err != nil {
				return nil, err
			}
			if r.Factories < in.K {
				r.Hidden = true
				r.CBM = 0
			}
			out = append(out, r)
		}
		return out, rows.Err()
	}
	if in.ByLane, err = group(`SELECT o.name || ' → ' || d.name, sum(b.cbm), count(DISTINCT b.company_id) ` +
		`FROM bookings b JOIN sailings s ON s.id=b.sailing_id JOIN lanes l ON l.id=s.lane_id JOIN ports o ON o.code=l.origin JOIN ports d ON d.code=l.dest
		WHERE b.status='booked' AND s.cfs_cutoff >= $1 AND s.cfs_cutoff < $2 GROUP BY 1 ORDER BY 2 DESC`); err != nil {
		return nil, err
	}
	if in.ByWeek, err = group(`SELECT 'Week ' || s.week, sum(b.cbm), count(DISTINCT b.company_id) ` + base + ` GROUP BY s.year, s.week ORDER BY s.year, s.week`); err != nil {
		return nil, err
	}
	if in.TopHS, err = group(`SELECT bl.hs_code || COALESCE(' · ' || h.description, ''), sum(bl.cbm), count(DISTINCT b.company_id)
		FROM booking_lines bl JOIN bookings b ON b.id=bl.booking_id JOIN sailings s ON s.id=b.sailing_id LEFT JOIN hs_codes h ON h.code=bl.hs_code
		WHERE b.status='booked' AND s.cfs_cutoff >= $1 AND s.cfs_cutoff < $2 GROUP BY bl.hs_code, h.description ORDER BY 2 DESC LIMIT 12`); err != nil {
		return nil, err
	}
	return in, nil
}
