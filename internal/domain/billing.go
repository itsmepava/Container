package domain

import (
	"math"
	"sort"
)

// Cents converts dollars to integer cents.
func Cents(usd float64) int64 { return int64(math.Round(usd * 100)) }

// SplitCents divides total cents across weights so the parts add up exactly
// (largest-remainder method). Weights must be non-negative.
func SplitCents(total int64, weights []float64) []int64 {
	out := make([]int64, len(weights))
	var sum float64
	for _, w := range weights {
		sum += w
	}
	if sum <= 0 || len(weights) == 0 {
		return out
	}
	type rem struct {
		i int
		r float64
	}
	rems := make([]rem, len(weights))
	var allocated int64
	for i, w := range weights {
		exact := float64(total) * w / sum
		fl := math.Floor(exact + 1e-9)
		out[i] = int64(fl)
		allocated += out[i]
		rems[i] = rem{i, exact - fl}
	}
	sort.SliceStable(rems, func(a, b int) bool { return rems[a].r > rems[b].r })
	for k := 0; allocated < total; k++ {
		out[rems[k%len(rems)].i]++
		allocated++
	}
	return out
}

// ShipperBill is one factory's line of the billing preview.
type ShipperBill struct {
	BookingID    int64
	WM           float64
	FreightCents int64
	FeeCents     int64
	TotalCents   int64 // after cap
	CapCents     int64
	CapApplied   bool  // total was reduced to the cap
	OverCapCents int64 // amount above cap before capping
}

// BillInput is one shipper in a pooled box.
type BillInput struct {
	BookingID int64
	WM        float64
	CapCents  int64 // 0 means no cap
}

// Bill splits the winning bid across shippers by W/M and adds the fee per W/M.
func Bill(bidCents int64, feePerWM float64, in []BillInput) []ShipperBill {
	w := make([]float64, len(in))
	for i, s := range in {
		w[i] = s.WM
	}
	shares := SplitCents(bidCents, w)
	out := make([]ShipperBill, len(in))
	for i, s := range in {
		fee := Cents(feePerWM * s.WM)
		total := shares[i] + fee
		b := ShipperBill{BookingID: s.BookingID, WM: s.WM, FreightCents: shares[i], FeeCents: fee, TotalCents: total, CapCents: s.CapCents}
		if s.CapCents > 0 && total > s.CapCents {
			b.OverCapCents = total - s.CapCents
			b.CapApplied = true
			b.TotalCents = s.CapCents
		}
		out[i] = b
	}
	return out
}

// CoLoadBill prices a co-loaded lot at the partner rate plus fee, capped.
func CoLoadBill(bookingID int64, wm, rate, feePerWM float64, capCents int64) ShipperBill {
	fr := Cents(rate * wm)
	fee := Cents(feePerWM * wm)
	b := ShipperBill{BookingID: bookingID, WM: wm, FreightCents: fr, FeeCents: fee, TotalCents: fr + fee, CapCents: capCents}
	if capCents > 0 && b.TotalCents > capCents {
		b.OverCapCents = b.TotalCents - capCents
		b.CapApplied = true
		b.TotalCents = capCents
	}
	return b
}

// BillSummary totals a billing preview.
type BillSummary struct {
	Shippers      int
	TotalWM       float64
	FreightPerWM  float64
	FeeCents      int64
	InvoicedCents int64
	AllInPerWM    float64
	AnyOverCap    bool
	MaxAllInPerWM float64
}

func Summarize(bidCents int64, bills []ShipperBill) BillSummary {
	s := BillSummary{Shippers: len(bills)}
	for _, b := range bills {
		s.TotalWM += b.WM
		s.FeeCents += b.FeeCents
		s.InvoicedCents += b.TotalCents
		if b.CapApplied {
			s.AnyOverCap = true
		}
		if b.WM > 0 {
			per := float64(b.FreightCents+b.FeeCents) / 100 / b.WM
			if per > s.MaxAllInPerWM {
				s.MaxAllInPerWM = per
			}
		}
	}
	s.TotalWM = Round(s.TotalWM, 3)
	if s.TotalWM > 0 {
		s.FreightPerWM = float64(bidCents) / 100 / s.TotalWM
		s.AllInPerWM = float64(bidCents+s.FeeCents) / 100 / s.TotalWM
	}
	return s
}
