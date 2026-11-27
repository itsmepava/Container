package domain

import (
	"strings"
	"testing"
)

func TestPlanFleet(t *testing.T) {
	lots := week49Lots()
	opts := []FleetOption{
		{ID: 1, Spec: spec("20GP"), Grade: GradeB, Cost: 1450},
		{ID: 2, Spec: spec("40HC"), Grade: GradeA, Cost: 2350},
		{ID: 3, Spec: spec("45HC"), Grade: GradeB, Cost: 2600},
		{ID: 4, Spec: spec("40GP"), Grade: GradeC, Cost: 1900},
	}
	plans, best := PlanFleet(opts, 0.85, lots, 50, 5)
	if best != 2 || !plans[2].Best {
		t.Fatalf("best = %d", best)
	}
	if l, c := plans[2].Status()[0], plans[2].Status()[1]; l != "Best · convert" || c != "b-best" {
		t.Fatalf("status %s %s", l, c)
	}
	// 45HC takes everything: 66.2 CBM, margin 66.2 × 45 − 2600.
	if len(plans[2].Fit) != 10 || plans[2].CBM != 66.2 || plans[2].Margin != 379 {
		t.Fatalf("45HC %+v", plans[2])
	}
	// 40HC: J (8.2) has no space left; booking order is kept.
	if len(plans[1].Rej) != 1 || plans[1].Rej[0].Lot.Label != "J" || plans[1].Rej[0].Why != "no space left" {
		t.Fatalf("40HC rej %+v", plans[1].Rej)
	}
	// 20GP is below break-even.
	if plans[0].Status()[0] != "Below break-even" {
		t.Fatalf("20GP %v", plans[0].Status())
	}
	// Grade C can't take fabric.
	g := plans[3].RejGroups()
	if len(g) == 0 || !strings.Contains(g[0].Why, "grade A or B") || len(g[0].Lots) != 6 {
		t.Fatalf("grade C groups %+v", g)
	}

	// Clash: dyes against fabric keeps the side with more CBM.
	mixed := []Lot{
		{ID: 1, Name: "Fab", CBM: 10, Kg: 1000, Category: CatFabric, Order: 0},
		{ID: 2, Name: "Dye", CBM: 3, Kg: 900, Category: CatDyes, Order: 1},
		{ID: 3, Name: "Trim", CBM: 2, Kg: 200, Category: CatTrims, Order: 2},
	}
	p := PlanFleetOption(FleetOption{Spec: spec("20GP"), Grade: GradeB, Cost: 500}, 0.85, mixed, 50, 5)
	if len(p.Fit) != 2 || len(p.Rej) != 1 || p.Rej[0].Lot.ID != 2 || p.Rej[0].Why != "dyes: can't share with fabric" {
		t.Fatalf("clash %+v", p)
	}
	// DG is never pooled.
	p = PlanFleetOption(FleetOption{Spec: spec("20GP"), Grade: GradeB, Cost: 500}, 0.85, []Lot{{ID: 9, CBM: 1, Kg: 10, Category: CatDG}}, 50, 5)
	if len(p.Fit) != 0 || p.Status()[0] != "Nothing allowed in" || !strings.HasPrefix(p.Rej[0].Why, "DG") {
		t.Fatalf("dg %+v", p)
	}
}
