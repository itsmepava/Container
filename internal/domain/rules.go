package domain

import (
	"fmt"
	"sort"
	"strings"
)

// Lot is one shipper's cargo as the planner sees it.
type Lot struct {
	ID       int64
	Label    string // "A", "B" … for the visualizer
	Name     string // shipper display name (ops only)
	CBM      float64
	Kg       float64
	Category Category
	Order    int // booking order; lower books first
}

// Box is a container being filled.
type Box struct {
	Spec   ContainerSpec
	Grade  Grade
	Usable float64
	Lots   []Lot
	Locked bool
}

func NewBox(spec ContainerSpec, grade Grade, stowage float64) *Box {
	return &Box{Spec: spec, Grade: grade, Usable: spec.UsableCBM(stowage)}
}

func (b *Box) UsedCBM() float64 {
	var s float64
	for _, l := range b.Lots {
		s += l.CBM
	}
	return Round(s, 2)
}

func (b *Box) UsedKg() float64 {
	var s float64
	for _, l := range b.Lots {
		s += l.Kg
	}
	return Round(s, 1)
}

func (b *Box) FreeCBM() float64 { return Round(b.Usable-b.UsedCBM(), 2) }

// FillPct is the share of usable space used, 0–100.
func (b *Box) FillPct() float64 {
	if b.Usable <= 0 {
		return 0
	}
	return b.UsedCBM() / b.Usable * 100
}

// GradeAllows reports whether a category may go in a box of this grade.
func GradeAllows(g Grade, c Category) error {
	switch c {
	case CatDG:
		return fmt.Errorf("dangerous goods aren't pooled in phase 1")
	case CatFabric:
		if g != GradeA && g != GradeB {
			return fmt.Errorf("fabric needs a clean grade A or B box")
		}
	case CatDyes:
		if g == GradeA {
			return fmt.Errorf("grade A boxes stay clean: no dyes or chemicals")
		}
	case CatMachinery:
		if g == GradeA {
			return fmt.Errorf("grade A boxes stay clean: no machinery parts")
		}
	}
	return nil
}

// Compatible reports whether two categories may share a box.
func Compatible(a, b Category) bool {
	if (a == CatDyes && b == CatFabric) || (a == CatFabric && b == CatDyes) {
		return false
	}
	if a == CatDG || b == CatDG {
		return false
	}
	return true
}

// CanPlace checks every loading rule for adding lot to box.
func (b *Box) CanPlace(l Lot) error {
	if err := GradeAllows(b.Grade, l.Category); err != nil {
		return err
	}
	for _, x := range b.Lots {
		if !Compatible(x.Category, l.Category) {
			return fmt.Errorf("%s can't share a box with %s", l.Category.Label(), x.Category.Label())
		}
	}
	if l.CBM > b.FreeCBM()+1e-9 {
		return fmt.Errorf("doesn't fit · %.1f CBM left", b.FreeCBM())
	}
	if b.UsedKg()+l.Kg > b.Spec.PayloadKg+1e-9 {
		return fmt.Errorf("over the box's %.1f t payload", b.Spec.PayloadKg/1000)
	}
	return nil
}

// Place adds the lot if every rule passes.
func (b *Box) Place(l Lot) error {
	if err := b.CanPlace(l); err != nil {
		return err
	}
	b.Lots = append(b.Lots, l)
	return nil
}

// Unplaced is a lot that didn't fit anywhere and why.
type Unplaced struct {
	Lot    Lot
	Reason string
}

// SortByOrder sorts lots into booking order.
func SortByOrder(lots []Lot) {
	sort.SliceStable(lots, func(i, j int) bool { return lots[i].Order < lots[j].Order })
}

// FirstFit places lots in booking order into the first box that takes them.
// A lot that doesn't fit is skipped, so later smaller lots can still fill gaps.
func FirstFit(boxes []*Box, lots []Lot) []Unplaced {
	lots = append([]Lot(nil), lots...)
	SortByOrder(lots)
	var out []Unplaced
	for _, l := range lots {
		placed := false
		var lastErr error
		for _, b := range boxes {
			if b.Locked {
				continue
			}
			if err := b.Place(l); err == nil {
				placed = true
				break
			} else {
				lastErr = err
			}
		}
		if !placed {
			reason := "no container on this sailing"
			if lastErr != nil {
				reason = lastErr.Error()
			}
			out = append(out, Unplaced{Lot: l, Reason: reason})
		}
	}
	return out
}

// LoadCheck is one line in the "Load and weight checks" list.
type LoadCheck struct {
	Label string
	OK    bool
}

// Checks summarises a box's rule checks for display.
func (b *Box) Checks() []LoadCheck {
	cats := map[Category]bool{}
	for _, l := range b.Lots {
		cats[l.Category] = true
	}
	var names []string
	for _, c := range Categories {
		if cats[c] {
			names = append(names, c.Short())
		}
	}
	mix := "Cargo mix compatible"
	if len(names) > 0 {
		mix = strings.Join(names, ", ") + ": compatible"
		mix = strings.ToUpper(mix[:1]) + mix[1:]
	}
	mixOK := true
	for i, x := range b.Lots {
		for _, y := range b.Lots[i+1:] {
			if !Compatible(x.Category, y.Category) {
				mixOK = false
			}
		}
		if GradeAllows(b.Grade, x.Category) != nil {
			mixOK = false
		}
	}
	return []LoadCheck{
		{Label: fmt.Sprintf("Volume %.1f / %.1f CBM", b.UsedCBM(), b.Usable), OK: b.UsedCBM() <= b.Usable+1e-9},
		{Label: fmt.Sprintf("Weight %.1f / %.1f t", b.UsedKg()/1000, b.Spec.PayloadKg/1000), OK: b.UsedKg() <= b.Spec.PayloadKg},
		{Label: mix, OK: mixOK},
		{Label: "No dangerous goods", OK: !cats[CatDG]},
	}
}

func joinAnd(xs []string) string {
	switch len(xs) {
	case 0:
		return ""
	case 1:
		return xs[0]
	}
	s := ""
	for i, x := range xs {
		if i > 0 {
			if i == len(xs)-1 {
				s += " and "
			} else {
				s += ", "
			}
		}
		s += x
	}
	return s
}
