package domain

import (
	"sort"
	"time"
)

// Inclusion is a charge every bid must include.
type Inclusion string

const (
	IncOcean       Inclusion = "ocean"
	IncOrigin      Inclusion = "origin"
	IncDestination Inclusion = "destination"
	IncDestuff     Inclusion = "destuff"
)

var Inclusions = []Inclusion{IncOcean, IncOrigin, IncDestination, IncDestuff}

func (i Inclusion) Label() string {
	switch i {
	case IncOcean:
		return "Ocean freight"
	case IncOrigin:
		return "Origin charges and CFS stuffing"
	case IncDestination:
		return "Destination THC and delivery order"
	case IncDestuff:
		return "De-stuffing at Colombo CFS"
	}
	return string(i)
}

// BidEval is a bid as the tender sees it.
type BidEval struct {
	ID          int64
	CompanyID   int64
	AmountCents int64
	VesselID    int64
	Includes    map[Inclusion]bool
	SubmittedAt time.Time
}

// Compliance says whether a bid meets the tender and, if not, why.
func Compliance(b BidEval, requestedVessel int64) (bool, string) {
	for _, inc := range Inclusions {
		if !b.Includes[inc] {
			return false, "Missing " + inc.Label()
		}
	}
	if requestedVessel != 0 && b.VesselID != requestedVessel {
		return false, "Quoted on another vessel, not the requested one"
	}
	if b.AmountCents <= 0 {
		return false, "No price"
	}
	return true, ""
}

// RankBids sorts bids cheapest first (earliest wins a tie) and returns the
// index of the auto pick: the lowest compliant bid, or -1 if none comply.
func RankBids(bids []BidEval, requestedVessel int64) (sorted []BidEval, autoPick int) {
	sorted = append([]BidEval(nil), bids...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].AmountCents != sorted[j].AmountCents {
			return sorted[i].AmountCents < sorted[j].AmountCents
		}
		return sorted[i].SubmittedAt.Before(sorted[j].SubmittedAt)
	})
	autoPick = -1
	for i, b := range sorted {
		if ok, _ := Compliance(b, requestedVessel); ok {
			autoPick = i
			break
		}
	}
	return sorted, autoPick
}
