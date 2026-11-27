package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/itsmepava/container/internal/domain"
)

// TestPlannerWorkflow covers the load planner's visualizer workflow.
func TestPlannerWorkflow(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()
	ops := userByEmail(t, a, "ops@eimsky.test")
	fac := userByEmail(t, a, "factory@eimsky.test")

	lanes, _ := a.Lanes(ctx, true)
	var sha *Lane
	for _, l := range lanes {
		if l.Origin.Code == "CNSHA" {
			sha = l
		}
	}
	opts, err := a.LaneOptions(ctx, sha.ID)
	if err != nil || len(opts) != 4 {
		t.Fatalf("options %v %v", opts, err)
	}
	if err := a.AddLaneOption(ctx, ops, sha.ID, "20GP", domain.GradeC, 0); err == nil {
		t.Fatal("zero cost accepted")
	}
	if err := a.AddLaneOption(ctx, ops, sha.ID, "20GP", domain.GradeC, 1300); err != nil {
		t.Fatal(err)
	}
	opts, _ = a.LaneOptions(ctx, sha.ID)
	if _, err := a.RemoveLaneOption(ctx, ops, opts[len(opts)-1].ID); err != nil {
		t.Fatal(err)
	}
	if err := a.SavePlannerMoney(ctx, ops, sha.ID, 85, 50, 60); err == nil {
		t.Fatal("handling above the selling price accepted")
	}
	if err := a.SavePlannerMoney(ctx, ops, sha.ID, 85, 50, 5); err != nil {
		t.Fatal(err)
	}

	// The week-50 planner sailing: the best option is the 45HC and nothing is converted yet.
	ss, _ := a.BookableSailings(ctx, sha.ID)
	var s *Sailing
	for _, x := range ss {
		if p, _ := a.Planner(ctx, x.ID); p != nil && len(p.Unplaced) > 0 {
			s = x
		}
	}
	if s == nil {
		t.Fatal("no sailing with overflow")
	}
	p, _ := a.Planner(ctx, s.ID)
	best := p.BestFleetPlan()
	if best == nil || best.Option.Spec.Code != "45HC" || p.Converted {
		t.Fatalf("best %+v converted %v", best, p.Converted)
	}

	// Ops adds a booking for a factory; the factory sees it; ops removes it.
	id, err := a.OpsAddBooking(ctx, ops, OpsBookingInput{SailingID: s.ID, CompanyID: fac.CompanyID, CBM: 1.2, Kg: 200, Category: domain.CatTrims})
	if err != nil {
		t.Fatal(err)
	}
	if b, err := a.BookingFor(ctx, fac, id); err != nil || b.Status != "booked" || b.CapCents != 6000 {
		t.Fatalf("ops booking %+v %v", b, err)
	}
	if _, err := a.OpsAddBooking(ctx, ops, OpsBookingInput{SailingID: s.ID, CompanyID: fac.CompanyID, CBM: 1, Kg: 10, Category: domain.CatDG}); err == nil {
		t.Fatal("DG booked")
	}
	if _, err := a.OpsRemoveBooking(ctx, ops, id); err != nil {
		t.Fatal(err)
	}

	// Convert to FCL with the best option: Container 1 becomes a 45HC grade B with every lot.
	msg, err := a.ConvertToFCL(ctx, ops, s.ID, best.Option.ID)
	if err != nil || !strings.Contains(msg, "45HC grade B") {
		t.Fatalf("%s %v", msg, err)
	}
	p, _ = a.Planner(ctx, s.ID)
	if !p.Converted || len(p.Unplaced) != 0 || p.PrimaryBox().Container.BoxCode != "45HC" {
		t.Fatalf("after convert: converted %v unplaced %d", p.Converted, len(p.Unplaced))
	}
	// Back to a 40HC: the last lot has no space left and can be rolled.
	var forty int64
	for _, f := range p.Fleet {
		if f.Option.Spec.Code == "40HC" {
			forty = f.Option.ID
		}
	}
	if _, err := a.ConvertToFCL(ctx, ops, s.ID, forty); err != nil {
		t.Fatal(err)
	}
	p, _ = a.Planner(ctx, s.ID)
	if len(p.Unplaced) != 1 || !p.Unplaced[0].Rollable() {
		t.Fatalf("unplaced %+v", p.Unplaced)
	}
	if err := a.Roll(ctx, ops, p.Unplaced[0].Booking.ID); err != nil {
		t.Fatal(err)
	}
	if m := a.PendingMoveFor(ctx, p.Unplaced[0].Booking.ID); m == nil || m.Target != "next_week" {
		t.Fatalf("roll %+v", m)
	}

	// Quick sailing: a week's sailing from vessel, voyage, ETD and cut-off days.
	etd := a.Now().AddDate(0, 0, 120)
	sid, err := a.AddSailingQuick(ctx, ops, QuickSailingInput{LaneID: sha.ID, Vessel: "MV Test Star", Voyage: "999W", ETD: etd, CutoffDays: 2})
	if err != nil {
		t.Fatal(err)
	}
	ns, _ := a.SailingByID(ctx, sid)
	if v := ns.Vessel(); v == nil || v.Name != "MV Test Star" || !ns.PlanLockAt.Before(ns.BidCloseAt) || ns.CFSCutoff.After(v.ETD) {
		t.Fatalf("quick sailing %+v", ns)
	}
	if _, err := a.AddSailingQuick(ctx, ops, QuickSailingInput{LaneID: sha.ID, Vessel: "X", Voyage: "1", ETD: a.Now().Add(-48 * time.Hour), CutoffDays: 2}); err == nil {
		t.Fatal("past ETD accepted")
	}
}
