package domain

import (
	"math"
	"testing"
	"time"
)

func near(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

// The nine lots of the Week 49 Shanghai pool, from the MVP screens.
func week49Lots() []Lot {
	data := []struct {
		l   string
		cbm float64
		kg  float64
		c   Category
	}{
		{"A", 6.6, 2016, CatFabric}, {"B", 9.2, 2100, CatFabric}, {"C", 7.4, 1500, CatTrims},
		{"D", 5.8, 1300, CatFabric}, {"E", 8.1, 1700, CatFabric}, {"F", 6.0, 1100, CatPackaging},
		{"G", 4.9, 800, CatTrims}, {"H", 5.5, 1000, CatFabric}, {"I", 4.5, 884, CatPackaging},
		{"J", 8.2, 1900, CatFabric},
	}
	var lots []Lot
	for i, d := range data {
		lots = append(lots, Lot{ID: int64(i + 1), Label: d.l, CBM: d.cbm, Kg: d.kg, Category: d.c, Order: i})
	}
	return lots
}

func spec(code string) ContainerSpec { s, _ := SpecByCode(DefaultContainerSpecs, code); return s }

func TestCargoLineCBM(t *testing.T) {
	lines := []CargoLine{
		{HSCode: "6006.32", Pieces: 40, LengthCm: 160, WidthCm: 30, HeightCm: 30, KgEach: 45},
		{HSCode: "9606.21", Pieces: 12, LengthCm: 50, WidthCm: 40, HeightCm: 35, KgEach: 18},
	}
	if lines[0].CBM() != 5.76 || lines[1].CBM() != 0.84 {
		t.Fatalf("cbm %v %v", lines[0].CBM(), lines[1].CBM())
	}
	p, cbm, kg := Totals(lines)
	if p != 52 || cbm != 6.6 || kg != 2016 {
		t.Fatalf("totals %d %v %v", p, cbm, kg)
	}
	if WM(cbm, kg) != 6.6 {
		t.Fatal("wm")
	}
	if WM(1, 2500) != 2.5 {
		t.Fatal("heavy wm should use tonnes")
	}
	for _, l := range lines {
		if err := l.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	bad := CargoLine{HSCode: "6006.32", Pieces: 1, LengthCm: 2000, WidthCm: 10, HeightCm: 10, KgEach: 1}
	if bad.Validate() == nil {
		t.Fatal("oversize piece accepted")
	}
}

func TestHSCode(t *testing.T) {
	if NormalizeHSCode("600632") != "6006.32" || NormalizeHSCode(" 9606.21 ") != "9606.21" || NormalizeHSCode("52084200") != "5208.42.00" {
		t.Fatal("normalize")
	}
	for _, ok := range []string{"6006.32", "600632", "5208.42.00"} {
		if ValidateHSCode(ok) != nil {
			t.Fatalf("%s rejected", ok)
		}
	}
	for _, bad := range []string{"", "60", "abc123", "9906.11", "0012.34", "6006.32.11.22", "6006321122"} {
		if ValidateHSCode(bad) == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestUsable(t *testing.T) {
	if spec("40HC").UsableCBM(0.85) != 64.6 {
		t.Fatal(spec("40HC").UsableCBM(0.85))
	}
	if spec("20GP").UsableCBM(0.85) != 28.2 {
		t.Fatal(spec("20GP").UsableCBM(0.85))
	}
}

func TestFirstFitWeek49(t *testing.T) {
	b := NewBox(spec("40HC"), GradeA, 0.85)
	un := FirstFit([]*Box{b}, week49Lots())
	if len(b.Lots) != 9 || b.UsedCBM() != 58.0 || b.FreeCBM() != 6.6 {
		t.Fatalf("lots %d used %v free %v", len(b.Lots), b.UsedCBM(), b.FreeCBM())
	}
	if !near(b.FillPct(), 89.8, 0.1) {
		t.Fatal(b.FillPct())
	}
	if b.UsedKg() != 12400 {
		t.Fatal(b.UsedKg())
	}
	if len(un) != 1 || un[0].Lot.Label != "J" || un[0].Reason != "doesn't fit · 6.6 CBM left" {
		t.Fatalf("%+v", un)
	}
	for _, c := range b.Checks() {
		if !c.OK {
			t.Fatalf("check failed %s", c.Label)
		}
	}
}

func TestRules(t *testing.T) {
	if GradeAllows(GradeC, CatFabric) == nil {
		t.Fatal("fabric in grade C")
	}
	if GradeAllows(GradeA, CatDyes) == nil {
		t.Fatal("dyes in grade A")
	}
	if GradeAllows(GradeB, CatDG) == nil {
		t.Fatal("DG pooled")
	}
	b := NewBox(spec("40HC"), GradeB, 0.85)
	if err := b.Place(Lot{CBM: 5, Kg: 100, Category: CatFabric}); err != nil {
		t.Fatal(err)
	}
	if b.Place(Lot{CBM: 1, Kg: 100, Category: CatDyes}) == nil {
		t.Fatal("dyes with fabric")
	}
	if b.Place(Lot{CBM: 1, Kg: 26000, Category: CatTrims}) == nil {
		t.Fatal("overweight accepted")
	}
}

func TestContainerOptions(t *testing.T) {
	bids := map[string]float64{"20GP": 1450, "40GP": 2200, "40HC": 2350, "45HC": 2600}
	opts := ContainerOptions(DefaultContainerSpecs, GradeA, 0.85, week49Lots(), bids)
	got := map[string]ContainerOption{}
	for _, o := range opts {
		got[o.Spec.Code] = o
	}
	want := map[string]struct {
		lots int
		fill float64
		per  float64
	}{
		"20GP": {4, 100, 51.6}, "40GP": {8, 93, 41.1}, "40HC": {9, 90, 40.5}, "45HC": {10, 91, 39.3},
	}
	for code, w := range want {
		o := got[code]
		if o.LotsFit != w.lots || math.Round(o.FillPct) != w.fill || Round(o.PerCBM, 1) != w.per {
			t.Fatalf("%s: lots %d fill %.1f per %.2f", code, o.LotsFit, o.FillPct, o.PerCBM)
		}
	}
	if !got["45HC"].Best {
		t.Fatal("45HC should be best fit")
	}
	if be := BreakEvenCBM(2350, 55); Round(be, 1) != 42.7 {
		t.Fatal(be)
	}
}

func TestPoolOptions(t *testing.T) {
	bids := map[string]float64{"20GP": 1450, "40HC": 2400}
	opts := PoolOptions(DefaultContainerSpecs, 0.85, 58.0, "40HC", bids, 55)
	if len(opts) != 3 {
		t.Fatalf("%+v", opts)
	}
	if math.Round(opts[0].PerCBM) != 41 || !opts[0].Recommended {
		t.Fatalf("own %+v", opts[0])
	}
	if opts[1].PerCBM != 55 {
		t.Fatal("coload")
	}
	if math.Round(opts[2].PerCBM) != 53 || opts[2].InBoxCBM != 28.2 || opts[2].CoLoadCBM != 29.8 {
		t.Fatalf("hybrid %+v", opts[2])
	}
}

func TestSplitCents(t *testing.T) {
	parts := SplitCents(100, []float64{1, 1, 1})
	if parts[0]+parts[1]+parts[2] != 100 {
		t.Fatal(parts)
	}
	ws := []float64{6.6, 9.2, 7.4, 5.8, 8.1, 6.0, 4.9, 5.5, 4.5}
	ps := SplitCents(235000, ws)
	var s int64
	for _, p := range ps {
		s += p
	}
	if s != 235000 || ps[0] != 26741 {
		t.Fatalf("sum %d first %d", s, ps[0])
	}
	if z := SplitCents(100, []float64{0, 0}); z[0] != 0 {
		t.Fatal("zero weights")
	}
}

func TestBillWorkedExample(t *testing.T) {
	ws := []float64{6.6, 9.2, 7.4, 5.8, 8.1, 6.0, 4.9, 5.5, 4.5}
	var in []BillInput
	for i, w := range ws {
		in = append(in, BillInput{BookingID: int64(i + 1), WM: w, CapCents: Cents(50 * w)})
	}
	bills := Bill(235000, 8, 0, in)
	a := bills[0]
	if a.FreightCents != 26741 || a.FeeCents != 5280 || a.TotalCents != 32021 || a.CapApplied {
		t.Fatalf("%+v", a)
	}
	sum := Summarize(235000, bills)
	if sum.FeeCents != 46400 || sum.InvoicedCents != 281400 || Round(sum.FreightPerWM, 2) != 40.52 || Round(sum.AllInPerWM, 2) != 48.52 || sum.AnyOverCap {
		t.Fatalf("%+v", sum)
	}
	// Forwarder B override at 2,410: 49.55 all-in, still under the cap.
	sumB := Summarize(241000, Bill(241000, 8, 0, in))
	if Round(sumB.FreightPerWM, 2) != 41.55 || sumB.AnyOverCap {
		t.Fatalf("%+v", sumB)
	}
	// A bid of 2,500 pushes shippers over the USD 50 cap.
	over := Bill(250000, 8, 0, in)
	if !over[0].CapApplied || over[0].TotalCents != 33000 {
		t.Fatalf("%+v", over[0])
	}
	c := CoLoadBill(1, 8.2, 55, 8, 0, Cents(50*8.2))
	if !c.CapApplied || c.TotalCents != 41000 {
		t.Fatalf("%+v", c)
	}
}

func TestRankBids(t *testing.T) {
	all := map[Inclusion]bool{IncOcean: true, IncOrigin: true, IncDestination: true, IncDestuff: true}
	now := time.Now()
	bids := []BidEval{
		{ID: 3, AmountCents: 248000, VesselID: 2, Includes: all, SubmittedAt: now},
		{ID: 1, AmountCents: 225000, VesselID: 1, Includes: all, SubmittedAt: now},
		{ID: 2, AmountCents: 235000, VesselID: 2, Includes: all, SubmittedAt: now},
		{ID: 4, AmountCents: 241000, VesselID: 2, Includes: all, SubmittedAt: now},
		{ID: 5, AmountCents: 200000, VesselID: 2, Includes: map[Inclusion]bool{IncOcean: true}, SubmittedAt: now},
	}
	sorted, pick := RankBids(bids, 2)
	if sorted[0].ID != 5 || sorted[pick].ID != 2 {
		t.Fatalf("pick %d %+v", pick, sorted)
	}
	if ok, why := Compliance(bids[1], 2); ok || why == "" {
		t.Fatal("wrong vessel compliant")
	}
	// Tie goes to the earlier bid.
	tie := []BidEval{
		{ID: 1, AmountCents: 100, VesselID: 2, Includes: all, SubmittedAt: now.Add(time.Minute)},
		{ID: 2, AmountCents: 100, VesselID: 2, Includes: all, SubmittedAt: now},
	}
	s, p := RankBids(tie, 2)
	if s[p].ID != 2 {
		t.Fatal("tie")
	}
}

func TestFormatting(t *testing.T) {
	if Money(32021) != "320.21" || Money(235000) != "2,350.00" || MoneyShort(235000) != "2,350" || Thousands(1234567) != "1,234,567" {
		t.Fatal("money")
	}
	ws := WeekStart(2026, 49)
	if ws.Format("2006-01-02") != "2026-11-30" || ws.Weekday() != time.Monday {
		t.Fatal(ws)
	}
	y, w := ISOWeek(ws.Add(time.Hour))
	if y != 2026 || w != 49 {
		t.Fatal(y, w)
	}
	now := time.Date(2026, 11, 25, 17, 0, 0, 0, Colombo)
	if Until(now, now.Add(5*24*time.Hour)) != "5 d" || Until(now, now.Add(30*time.Hour)) != "1 d 6 h" || Until(now, now.Add(-time.Hour)) != "passed" {
		t.Fatal(Until(now, now.Add(30*time.Hour)))
	}
}

func TestHandlingFeeAndMachinery(t *testing.T) {
	in := []BillInput{{BookingID: 1, WM: 6.6}, {BookingID: 2, WM: 3.4}}
	bills := Bill(100000, 8, 1500, in)
	if bills[0].HandlingCents != 1500 || bills[0].TotalCents != bills[0].FreightCents+bills[0].FeeCents+1500 {
		t.Fatalf("%+v", bills[0])
	}
	if s := Summarize(100000, bills); s.InvoicedCents != 100000+Cents(8*10)+3000 {
		t.Fatalf("%+v", s)
	}
	if GradeAllows(GradeA, CatMachinery) == nil || GradeAllows(GradeB, CatMachinery) != nil {
		t.Fatal("machinery grade rule")
	}
	if !CatMachinery.Valid() || CatMachinery.Label() != "Machinery parts" {
		t.Fatal("machinery category")
	}
}
