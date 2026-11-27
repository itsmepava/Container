package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/itsmepava/container/internal/domain"
)

// TestPlanGaps covers the MVP plan items added after the first build.
func TestPlanGaps(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()
	admin := userByEmail(t, a, "admin@eimsky.test")
	ops := userByEmail(t, a, "ops@eimsky.test")
	fac := userByEmail(t, a, "factory@eimsky.test")

	// Settings: handling fee, LKR rate, strict HS check.
	if err := a.SaveSettings(ctx, admin, map[string]string{"handling_fee": "15", "usd_lkr_rate": "300", "hs_check": "block"}); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveSettings(ctx, admin, map[string]string{"hs_check": "maybe"}); err == nil {
		t.Fatal("bad hs_check accepted")
	}
	st := a.Settings(ctx)
	if st.HandlingCents != 1500 || st.OwnBoxThreshold != 65 || st.DecisionHours != 48 || st.BidCloseHours != 24 {
		t.Fatalf("%+v", st)
	}

	// HS list: an unknown code blocks the booking until the list is imported.
	lanes, _ := a.Lanes(ctx, true)
	var sha *Lane
	for _, l := range lanes {
		if l.Origin.Code == "CNSHA" {
			sha = l
		}
	}
	if sha.FallbackID == 0 || sha.Fallback["40HC"] != 2600 {
		t.Fatalf("fallback not seeded: %+v", sha)
	}
	ss, _ := a.BookableSailings(ctx, sha.ID)
	target := ss[len(ss)-1]
	in := QuoteInput{SailingID: target.ID, BoxCode: "40HC", Category: domain.CatMachinery, Lines: []domain.CargoLine{{HSCode: "8452.90", Pieces: 4, LengthCm: 100, WidthCm: 80, HeightCm: 60, KgEach: 120}}}
	if q, _ := a.Quote(ctx, in); q.CanBook {
		t.Fatal("unknown HS code bookable in block mode")
	}
	if _, _, err := a.ImportHSCodes(ctx, admin, [][]string{{"code", "description"}, {"845290", "Sewing machine parts"}, {"bad", "x"}}); err != nil {
		t.Fatal(err)
	}
	q, _ := a.Quote(ctx, in)
	for _, pr := range q.Problems {
		if strings.Contains(pr, "tariff list") {
			t.Fatalf("imported code still unknown: %v", q.Problems)
		}
	}
	// Machinery isn't allowed in a grade A box (the Shanghai default), so swap the grade.
	cs, _ := a.Containers(ctx, a.DB, target.ID)
	if _, err := a.UpdateContainer(ctx, ops, cs[0].ID, "40HC", domain.GradeB); err != nil {
		t.Fatal(err)
	}
	q, _ = a.Quote(ctx, in)
	if !q.CanBook {
		t.Fatalf("machinery booking refused on grade B: %v", q.Problems)
	}

	// LKR invoicing and the handling fee.
	if err := a.UpdateCompanyProfile(ctx, fac, fac.CompanyID, fac.CompanyName, "PV-1", "Colombo", "", "LKR"); err != nil {
		t.Fatal(err)
	}
	held, err := a.HoldBooking(ctx, fac, BookingInput{QuoteInput: in})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ConfirmBooking(ctx, fac, held.ID); err != nil {
		t.Fatal(err)
	}

	// Threshold: the target week is far below 65% → own box not recommended, roll offered.
	p, _ := a.Planner(ctx, target.ID)
	if !p.Below {
		t.Fatalf("fill %.0f%% should be below threshold", p.PrimaryFill)
	}
	for _, o := range p.PoolOptions {
		if o.Key == "own" && o.Recommended {
			t.Fatal("own box recommended below threshold")
		}
	}

	// Finalize this week; with no bids, the tender falls back to the pre-agreed rate.
	tids, err := a.LockAndTender(ctx, ops, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	a.Now = func() time.Time { return time.Now().Add(30 * 24 * time.Hour) }
	if _, err := a.CloseDueTenders(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.AwardFallback(ctx, ops, tids[0]); err != nil {
		t.Fatal(err)
	}
	a.Now = time.Now
	tv, _ := a.TenderView(ctx, tids[0], 0)
	if tv.Tender.AwardKind != "fallback" || tv.Selected.Bid.AmountCents != 260000 || tv.Selected.Bid.CompanyID != sha.FallbackID {
		t.Fatalf("fallback award %+v", tv.Tender)
	}
	b, _ := a.BookingFor(ctx, fac, held.ID)
	if b.HandlingCents == nil || *b.HandlingCents != 1500 {
		t.Fatalf("handling fee not charged: %+v", b.HandlingCents)
	}
	if _, err := a.UploadDoc(ctx, ops, "booking", b.ID, "commercial_invoice", "ci.pdf", demoPDF("ci"), "application/pdf"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.UploadDoc(ctx, ops, "tender", tids[0], "booking_confirmation", "bc.pdf", demoPDF("bc"), "application/pdf"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.IssueDueInvoices(ctx); err != nil {
		t.Fatal(err)
	}
	inv, err := a.InvoiceForBooking(ctx, fac, b.ID)
	if err != nil || inv.Currency != "LKR" || inv.TotalLocal != inv.TotalCents*300 || inv.HandlingCents != 1500 {
		t.Fatalf("LKR invoice %v %+v", err, inv)
	}
	if pdf, err := a.InvoicePDF(ctx, fac, inv); err != nil || !strings.HasPrefix(string(pdf), "%PDF") {
		t.Fatal(err)
	}

	// Payables: the fallback forwarder is owed the box; mark it paid.
	ps, perr := a.Payables(ctx, "due")
	if perr != nil {
		t.Fatal(perr)
	}
	found := false
	for _, x := range ps {
		if x.TenderID == tids[0] && x.AmountCents == 260000 && x.Confirmation && !x.BLCopy {
			found = true
		}
	}
	if !found {
		t.Fatalf("payable missing: %+v", ps)
	}
	if err := a.MarkPayout(ctx, ops, tids[0], true, "BOC-123"); err != nil {
		t.Fatal(err)
	}
	if ps, _ := a.Payables(ctx, "paid"); len(ps) == 0 || ps[0].PaidRef != "BOC-123" {
		t.Fatalf("paid %+v", ps)
	}

	// Overdue B/L copy after 24 h: forwarder pushed, ops told.
	a.Now = func() time.Time { return time.Now().Add(26 * time.Hour) }
	n, err := a.SendAwardDocReminders(ctx)
	a.Now = time.Now
	if err != nil || n == 0 {
		t.Fatalf("award doc reminders %d %v", n, err)
	}

	// Roll: move a whole open sailing to the next week.
	var chennai *Lane
	for _, l := range lanes {
		if l.Origin.Code == "INMAA" {
			chennai = l
		}
	}
	css, _ := a.BookableSailings(ctx, chennai.ID)
	if len(css) == 0 {
		t.Fatal("no chennai sailing")
	}
	if _, err := a.CopySailingToNextWeek(ctx, admin, css[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := a.Decide(ctx, ops, css[0].ID, "roll"); err != nil {
		t.Fatal(err)
	}
	rolled, _ := a.SailingByID(ctx, css[0].ID)
	if rolled.Decision != "roll" || rolled.Status != "closed" {
		t.Fatalf("roll %+v", rolled)
	}
	if left, _ := a.SearchBookings(ctx, "", "booked", css[0].ID, 10); len(left) != 0 {
		t.Fatalf("%d bookings left behind", len(left))
	}

	// Co-load a tender that got no bids.
	open, _ := a.TenderList(ctx, "open")
	if len(open) > 0 {
		if err := a.ColoadTender(ctx, ops, open[0].ID); err != nil {
			t.Fatal(err)
		}
		tv, _ := a.TenderView(ctx, open[0].ID, 0)
		if tv.Tender.Status != "cancelled" || tv.Tender.AwardKind != "coload" {
			t.Fatalf("%+v", tv.Tender)
		}
	}
}
