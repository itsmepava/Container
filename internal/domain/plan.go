package domain

import "math"

// ContainerOption is one row of "Container options for these requests".
type ContainerOption struct {
	Spec        ContainerSpec
	Usable      float64
	LotsFit     int
	PlacedCBM   float64
	FillPct     float64
	ExpectedBid float64 // USD per box
	PerCBM      float64 // freight per CBM if this box is used
	Best        bool
	Box         *Box       // the simulated load
	Unplaced    []Unplaced // lots that don't go in, with why
	Grade       Grade
}

// ContainerOptions simulates filling one box of each type with the lots, in
// booking order, and prices each against its expected bid.
func ContainerOptions(specs []ContainerSpec, grade Grade, stowage float64, lots []Lot, expectedBid map[string]float64) []ContainerOption {
	var out []ContainerOption
	bestIdx := -1
	for _, s := range specs {
		bid, ok := expectedBid[s.Code]
		if !ok || bid <= 0 {
			continue
		}
		b := NewBox(s, grade, stowage)
		un := FirstFit([]*Box{b}, lots)
		o := ContainerOption{Spec: s, Usable: b.Usable, LotsFit: len(b.Lots), PlacedCBM: b.UsedCBM(), FillPct: b.FillPct(), ExpectedBid: bid, Box: b, Unplaced: un, Grade: grade}
		if o.PlacedCBM > 0 {
			o.PerCBM = bid / o.PlacedCBM
		}
		out = append(out, o)
	}
	// Best fit: fits the most lots; among those the lowest cost per CBM.
	for i, o := range out {
		if o.PlacedCBM == 0 {
			continue
		}
		if bestIdx < 0 || o.LotsFit > out[bestIdx].LotsFit || (o.LotsFit == out[bestIdx].LotsFit && o.PerCBM < out[bestIdx].PerCBM) {
			bestIdx = i
		}
	}
	if bestIdx >= 0 {
		out[bestIdx].Best = true
	}
	return out
}

// BreakEvenCBM is the volume at which an own box costs the same per CBM as co-loading.
func BreakEvenCBM(expectedBid, coloadRate float64) float64 {
	if coloadRate <= 0 {
		return math.Inf(1)
	}
	return expectedBid / coloadRate
}

// PoolOption is one of the choices ops makes for a sailing.
type PoolOption struct {
	Key         string // own, coload, hybrid
	Title       string
	PerCBM      float64
	Detail      string
	Recommended bool
	BoxCode     string
	InBoxCBM    float64
	CoLoadCBM   float64
}

// PoolOptions compares an own box, co-loading everything, and the smallest
// box plus co-loading the rest, for the total requested volume.
func PoolOptions(specs []ContainerSpec, stowage float64, totalCBM float64, ownCode string, expectedBid map[string]float64, coloadRate float64) []PoolOption {
	var opts []PoolOption
	if totalCBM <= 0 {
		return opts
	}
	if spec, ok := SpecByCode(specs, ownCode); ok {
		usable := spec.UsableCBM(stowage)
		in := math.Min(totalCBM, usable)
		over := Round(totalCBM-in, 2)
		bid := expectedBid[ownCode]
		per := (bid + over*coloadRate) / totalCBM
		d := "Expected bid about USD " + fmtInt(bid) + " · " + fmtPct(in/usable*100) + " full"
		if over > 0 {
			d += " · " + fmtNum(over) + " CBM co-loaded"
		}
		opts = append(opts, PoolOption{Key: "own", Title: "Own " + ownCode, PerCBM: per, Detail: d, BoxCode: ownCode, InBoxCBM: in, CoLoadCBM: over})
	}
	opts = append(opts, PoolOption{Key: "coload", Title: "Co-load everything", PerCBM: coloadRate, Detail: "Partner LCL rate · no bidding", CoLoadCBM: totalCBM})
	// Hybrid: the smallest box smaller than the own box, plus co-load.
	var small *ContainerSpec
	for i := range specs {
		s := specs[i]
		if s.Code == ownCode || expectedBid[s.Code] <= 0 {
			continue
		}
		if small == nil || s.InternalCBM < small.InternalCBM {
			small = &s
		}
	}
	if small != nil {
		usable := small.UsableCBM(stowage)
		if usable < totalCBM {
			over := Round(totalCBM-usable, 2)
			per := (expectedBid[small.Code] + over*coloadRate) / totalCBM
			opts = append(opts, PoolOption{Key: "hybrid", Title: "Own " + small.Code + " + co-load rest", PerCBM: per,
				Detail: fmtNum(usable) + " CBM in box · " + fmtNum(over) + " CBM co-loaded", BoxCode: small.Code, InBoxCBM: usable, CoLoadCBM: over})
		}
	}
	best := 0
	for i := range opts {
		if opts[i].PerCBM < opts[best].PerCBM {
			best = i
		}
	}
	opts[best].Recommended = true
	return opts
}

// UnplacedGroup is a set of lots left out for the same reason.
type UnplacedGroup struct {
	Reason string
	Lots   []Lot
}

// GroupUnplaced groups lots that don't fit by reason, keeping first-seen order.
func GroupUnplaced(un []Unplaced) []UnplacedGroup {
	var out []UnplacedGroup
	idx := map[string]int{}
	for _, u := range un {
		i, ok := idx[u.Reason]
		if !ok {
			i = len(out)
			idx[u.Reason] = i
			out = append(out, UnplacedGroup{Reason: u.Reason})
		}
		out[i].Lots = append(out[i].Lots, u.Lot)
	}
	return out
}

// SavingVsCoload is what one box of this option saves against sending what it
// carries to the co-load partner (negative: co-load is cheaper).
func (o ContainerOption) SavingVsCoload(coloadRate float64) float64 {
	return Round(o.PlacedCBM*coloadRate-o.ExpectedBid, 0)
}

// LoadedKg is the simulated weight in the box.
func (o ContainerOption) LoadedKg() float64 {
	if o.Box == nil {
		return 0
	}
	return o.Box.UsedKg()
}
