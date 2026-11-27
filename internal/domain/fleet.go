package domain

import (
	"fmt"
	"math"
)

// FleetOption is one container option on a lane: a type, a grade and its
// all-in cost (freight, THC, CFS stuffing).
type FleetOption struct {
	ID    int64
	Spec  ContainerSpec
	Grade Grade
	Cost  float64 // USD per box
}

// Rejected is a lot an option can't load, with a short reason.
type Rejected struct {
	Lot Lot
	Why string
}

// FleetPlan is the simulated load of one fleet option, as the load
// planner shows it: what fits, what doesn't and why, and the money.
type FleetPlan struct {
	Option    FleetOption
	Nominal   float64 // internal CBM
	Usable    float64 // after stowage
	CBM, Kg   float64 // loaded
	WM        float64 // loaded W/M
	Fit       []Lot
	Rej       []Rejected
	BreakEven float64 // W/M at which the box pays for itself; +Inf if never
	Margin    float64 // WM × (sell − handling) − cost
	Fill      float64 // CBM / usable, 0–1
	WFill     float64 // kg / payload, 0–1
	Best      bool
}

// Status is the card badge: nothing allowed, below break-even, best, profitable.
// It returns the label and its CSS class.
func (p FleetPlan) Status() [2]string {
	switch {
	case len(p.Fit) == 0:
		return [2]string{"Nothing allowed in", "b-bad"}
	case p.Margin <= 0:
		return [2]string{"Below break-even", "b-warn"}
	case p.Best:
		return [2]string{"Best · convert", "b-best"}
	}
	return [2]string{"Profitable", "b-ok"}
}

// RejGroup is rejected lots sharing a reason.
type RejGroup struct {
	Why  string
	Lots []Lot
}

func (p FleetPlan) RejGroups() []RejGroup {
	var out []RejGroup
	idx := map[string]int{}
	for _, r := range p.Rej {
		i, ok := idx[r.Why]
		if !ok {
			i = len(out)
			idx[r.Why] = i
			out = append(out, RejGroup{Why: r.Why})
		}
		out[i].Lots = append(out[i].Lots, r.Lot)
	}
	return out
}

// WhyNot gives the short reason a lot can't go in a box of this grade, or "".
func WhyNot(c Category, g Grade) string {
	if c == CatDG {
		return "DG: book a separate DG-approved box"
	}
	if err := GradeAllows(g, c); err != nil {
		switch c {
		case CatFabric:
			return "needs grade A or B (clean)"
		default:
			return "grade A boxes stay clean"
		}
	}
	return ""
}

func fillLots(cands []Lot, usable, payload float64) (cbm, kg float64, fit []Lot, rej []Rejected) {
	for _, l := range cands {
		if cbm+l.CBM <= usable+1e-9 && kg+l.Kg <= payload {
			cbm += l.CBM
			kg += l.Kg
			fit = append(fit, l)
		} else {
			rej = append(rej, Rejected{l, "no space left"})
		}
	}
	return
}

// PlanFleetOption fills one box of the option with the lots in booking
// order. Lots its grade can't carry are rejected first; when the remaining
// lots include categories that can't share a box, it keeps whichever side
// loads more CBM and rejects the other side.
func PlanFleetOption(o FleetOption, stowage float64, lots []Lot, sellPerWM, handlingPerWM float64) FleetPlan {
	lots = append([]Lot(nil), lots...)
	SortByOrder(lots)
	p := FleetPlan{Option: o, Nominal: o.Spec.InternalCBM, Usable: o.Spec.UsableCBM(stowage)}
	var ok []Lot
	for _, l := range lots {
		if w := WhyNot(l.Category, o.Grade); w != "" {
			p.Rej = append(p.Rej, Rejected{l, w})
		} else {
			ok = append(ok, l)
		}
	}
	// Candidate sides: everything, or for each category present, the lots
	// that don't clash with it.
	present := map[Category]bool{}
	for _, l := range ok {
		present[l.Category] = true
	}
	clash := false
	for a := range present {
		for b := range present {
			if a != b && !Compatible(a, b) {
				clash = true
			}
		}
	}
	type side struct {
		keep      Category
		why       map[int64]Category
		cbm, kg   float64
		fit, left []Lot
		rej       []Rejected
	}
	var best *side
	if !clash {
		s := &side{}
		s.cbm, s.kg, s.fit, s.rej = fillLots(ok, p.Usable, o.Spec.PayloadKg)
		best = s
	} else {
		for _, c := range Categories {
			if !present[c] {
				continue
			}
			// Keep c, then every lot (in booking order) that gets on with
			// everything kept so far.
			s := &side{keep: c, why: map[int64]Category{}}
			kept := map[Category]bool{c: true}
			var cand []Lot
			for _, l := range ok {
				clashWith := Category("")
				for _, k := range Categories {
					if kept[k] && k != l.Category && !Compatible(k, l.Category) {
						clashWith = k
						break
					}
				}
				if clashWith == "" {
					cand = append(cand, l)
					kept[l.Category] = true
				} else {
					s.left = append(s.left, l)
					s.why[l.ID] = clashWith
				}
			}
			s.cbm, s.kg, s.fit, s.rej = fillLots(cand, p.Usable, o.Spec.PayloadKg)
			if best == nil || s.cbm > best.cbm {
				best = s
			}
		}
		for _, l := range best.left {
			p.Rej = append(p.Rej, Rejected{l, fmt.Sprintf("%s: can't share with %s", l.Category.Short(), best.why[l.ID].Short())})
		}
	}
	p.Rej = append(p.Rej, best.rej...)
	p.CBM, p.Kg, p.Fit = Round(best.cbm, 2), Round(best.kg, 1), best.fit
	for _, l := range p.Fit {
		p.WM += WM(l.CBM, l.Kg)
	}
	p.WM = Round(p.WM, 2)
	unit := sellPerWM - handlingPerWM
	p.BreakEven = math.Inf(1)
	if unit > 0 {
		p.BreakEven = o.Cost / unit
	}
	p.Margin = Round(p.WM*unit-o.Cost, 0)
	if p.Usable > 0 {
		p.Fill = p.CBM / p.Usable
	}
	if o.Spec.PayloadKg > 0 {
		p.WFill = p.Kg / o.Spec.PayloadKg
	}
	return p
}

// PlanFleet plans every option and marks the one with the highest positive
// margin as best. It returns the plans and the best one's index (-1: none).
func PlanFleet(opts []FleetOption, stowage float64, lots []Lot, sellPerWM, handlingPerWM float64) ([]FleetPlan, int) {
	out := make([]FleetPlan, len(opts))
	best := -1
	for i, o := range opts {
		out[i] = PlanFleetOption(o, stowage, lots, sellPerWM, handlingPerWM)
		if out[i].Margin > 0 && len(out[i].Fit) > 0 && (best < 0 || out[i].Margin > out[best].Margin) {
			best = i
		}
	}
	if best >= 0 {
		out[best].Best = true
	}
	return out, best
}
