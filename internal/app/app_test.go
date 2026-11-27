package app

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/itsmepava/container/internal/config"
	"github.com/itsmepava/container/internal/db"
	"github.com/itsmepava/container/internal/domain"
	"github.com/itsmepava/container/internal/files"
)

// testApp resets the test database and seeds the demo.
func testApp(t *testing.T) *App {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := db.Reset(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	fs, err := files.New(t.TempDir(), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	BcryptCost = 4
	cfg := config.Config{BaseURL: "http://test", DevMode: true}
	a := New(pool, fs, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := a.SeedDemo(ctx); err != nil {
		t.Fatal(err)
	}
	return a
}

func userByEmail(t *testing.T, a *App, email string) *User {
	t.Helper()
	u, err := a.Login(context.Background(), email, DemoPassword)
	if err != nil {
		t.Fatal(email, err)
	}
	return u
}

func forwarderByPhone(t *testing.T, a *App, phone string) *User {
	t.Helper()
	ctx := context.Background()
	_, code, err := a.RequestOTP(ctx, phone)
	if err != nil {
		t.Fatal(err)
	}
	u, _, err := a.VerifyOTP(ctx, phone, code)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestFullFlow(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()
	ops := userByEmail(t, a, "ops@eimsky.test")
	fac := userByEmail(t, a, "factory@eimsky.test")

	// 1. The closed Shanghai tender: auto pick is Forwarder A at 2,350 on the fast vessel.
	ts, err := a.TenderList(ctx, "closed")
	if err != nil || len(ts) != 1 {
		t.Fatalf("closed tenders %d %v", len(ts), err)
	}
	tv, err := a.TenderView(ctx, ts[0].ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if tv.AutoPick == nil || tv.AutoPick.Bid.AmountCents != 235000 || tv.AutoPick.Bid.CompanyName != "Blue Harbour Logistics" {
		t.Fatalf("auto pick %+v", tv.AutoPick)
	}
	if tv.Bids[0].Compliant || !strings.Contains(tv.CheaperNote, "saves USD 100") {
		t.Fatalf("alt vessel bid should be flagged: %+v / %q", tv.Bids[0], tv.CheaperNote)
	}
	if domain.Round(tv.Summary.FreightPerWM, 2) != 40.52 || tv.Summary.FeeCents != 46400 || tv.Summary.InvoicedCents != 281400 {
		t.Fatalf("summary %+v", tv.Summary)
	}
	// An override without a reason is refused.
	var bidB int64
	for _, r := range tv.Bids {
		if r.Bid.AmountCents == 241000 {
			bidB = r.Bid.ID
		}
	}
	if err := a.Award(ctx, ops, ts[0].ID, bidB, ""); err == nil {
		t.Fatal("override without reason accepted")
	}
	// An override that breaks a cap is blocked (Forwarder C at 2,480 → 50.76 per W/M > 50).
	var bidC int64
	for _, r := range tv.Bids {
		if r.Bid.AmountCents == 248000 {
			bidC = r.Bid.ID
		}
	}
	if err := a.Award(ctx, ops, ts[0].ID, bidC, "better service"); err == nil || !strings.Contains(err.Error(), "cap") {
		t.Fatalf("cap breach not blocked: %v", err)
	}
	if err := a.Award(ctx, ops, ts[0].ID, tv.AutoPick.Bid.ID, ""); err != nil {
		t.Fatal(err)
	}
	// Factory A's worked example: USD 320.21.
	bs, _ := a.CompanyBookings(ctx, fac.CompanyID, false)
	var worked *Booking
	for _, b := range bs {
		if b.SailingID == tv.Tender.Sailing.ID {
			worked = b
		}
	}
	if worked == nil || worked.TotalCents == nil || *worked.TotalCents != 32021 || *worked.FreightCents != 26741 {
		t.Fatalf("worked example %+v", worked)
	}
	steps := a.Progress(ctx, worked)
	if !steps[3].Done || steps[4].Done || !steps[4].Active {
		t.Fatalf("progress %+v", steps)
	}

	// 2. Forwarder A uploads the booking confirmation; Factory A's invoice is issued.
	fwA := forwarderByPhone(t, a, "0770000001")
	if fwA.CompanyName != "Blue Harbour Logistics" {
		t.Fatal(fwA.CompanyName)
	}
	ad, err := a.AwardDetailFor(ctx, fwA, ts[0].ID)
	if err != nil || len(ad.Shippers) != 9 || ad.Shippers[0].ContactEmail == "" {
		t.Fatalf("award detail %v %+v", err, ad)
	}
	if _, data, err := a.OpenDoc(ctx, fwA, ad.Shippers[0].Docs[0].ID); err != nil || len(data) == 0 {
		t.Fatalf("winner can't read shipper doc: %v", err)
	}
	fwB := forwarderByPhone(t, a, "+94770000002")
	if _, _, err := a.OpenDoc(ctx, fwB, ad.Shippers[0].Docs[0].ID); err == nil {
		t.Fatal("losing forwarder read a shipper document")
	}
	if _, err := a.AwardDetailFor(ctx, fwB, ts[0].ID); err == nil {
		t.Fatal("loser saw award detail")
	}
	if _, err := a.UploadDoc(ctx, fwA, "tender", ts[0].ID, "booking_confirmation", "conf.pdf", demoPDF("conf"), "application/pdf"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.IssueDueInvoices(ctx); err != nil {
		t.Fatal(err)
	}
	inv, err := a.InvoiceForBooking(ctx, fac, worked.ID)
	if err != nil || inv.TotalCents != 32021 {
		t.Fatalf("invoice %v %+v", err, inv)
	}
	pdf, err := a.InvoicePDF(ctx, fac, inv)
	if err != nil || !strings.HasPrefix(string(pdf), "%PDF") {
		t.Fatal("pdf", err)
	}

	// 3. Forwarder bidding on the open Ningbo tender.
	open, err := a.ForwarderTenders(ctx, fwA, "open")
	if err != nil || len(open) != 1 {
		t.Fatalf("open tenders %d %v", len(open), err)
	}
	all := map[domain.Inclusion]bool{domain.IncOcean: true, domain.IncOrigin: true, domain.IncDestination: true, domain.IncDestuff: true}
	if _, err := a.PlaceBid(ctx, fwA, BidInput{TenderID: open[0].ID, AmountCents: 195000, Includes: all}); err != nil {
		t.Fatal(err)
	}
	b, err := a.PlaceBid(ctx, fwA, BidInput{TenderID: open[0].ID, AmountCents: 194000, Includes: all})
	if err != nil || b.AmountCents != 194000 {
		t.Fatal("update bid", err)
	}
	// Award must refuse while bids are open.
	if err := a.Award(ctx, ops, open[0].ID, b.ID, ""); err == nil {
		t.Fatal("award before close accepted")
	}

	// 4. A new booking: quote, hold, confirm, auto-placement.
	lanes, _ := a.Lanes(ctx, true)
	var sha *Lane
	for _, l := range lanes {
		if l.Origin.Code == "CNSHA" {
			sha = l
		}
	}
	ss, err := a.BookableSailings(ctx, sha.ID)
	if err != nil || len(ss) < 2 {
		t.Fatalf("bookable sailings %d %v", len(ss), err)
	}
	target := ss[len(ss)-1] // week +3, plenty of space
	in := BookingInput{QuoteInput: QuoteInput{SailingID: target.ID, BoxCode: "40HC", Category: domain.CatFabric, Lines: []domain.CargoLine{
		{HSCode: "600632", Pieces: 40, LengthCm: 160, WidthCm: 30, HeightCm: 30, KgEach: 45},
	}}}
	qt, err := a.Quote(ctx, in.QuoteInput)
	if err != nil || !qt.CanBook || qt.CBM != 5.76 || qt.CapCents != 28800 {
		t.Fatalf("quote %v %+v", err, qt)
	}
	held, err := a.HoldBooking(ctx, fac, in)
	if err != nil || held.Status != "held" {
		t.Fatal(err)
	}
	conf, err := a.ConfirmBooking(ctx, fac, held.ID)
	if err != nil || conf.Status != "booked" || conf.ContainerID == 0 {
		t.Fatalf("confirm %v %+v", err, conf)
	}
	// Another factory can't see it.
	other := userByEmail(t, a, "factory2@eimsky.test")
	if _, err := a.BookingFor(ctx, other, conf.ID); err == nil {
		t.Fatal("other factory saw booking")
	}
	// DG is refused; dyes can't join a fabric box.
	in.Category = domain.CatDG
	if q, _ := a.Quote(ctx, in.QuoteInput); q.CanBook {
		t.Fatal("DG bookable")
	}
	in.Category = domain.CatDyes
	if q, _ := a.Quote(ctx, in.QuoteInput); q.CanBook {
		t.Fatal("dyes into fabric grade A box")
	}
	// Oversized booking needs AllowOverflow.
	big := BookingInput{QuoteInput: QuoteInput{SailingID: target.ID, BoxCode: "40HC", Category: domain.CatTrims, Lines: []domain.CargoLine{
		{HSCode: "9607.11", Pieces: 600, LengthCm: 50, WidthCm: 40, HeightCm: 50, KgEach: 10},
	}}}
	if _, err := a.HoldBooking(ctx, fac, big); err == nil {
		t.Fatal("overflow booked without consent")
	}

	// 5. Planner for Shanghai week +2: J doesn't fit.
	var planSailing *Sailing
	for _, s := range ss {
		p, _ := a.Planner(ctx, s.ID)
		if len(p.Unplaced) == 1 {
			planSailing = s
		}
	}
	if planSailing == nil {
		t.Fatal("no planner sailing with one overflow")
	}
	p, _ := a.Planner(ctx, planSailing.ID)
	if p.Boxes[0].Box.UsedCBM() != 58.0 || p.Unplaced[0].Booking.CBM != 8.2 || p.Unplaced[0].Reason != "doesn't fit · 6.6 CBM left" || p.CanLock() {
		t.Fatalf("planner %v %+v", p.Boxes[0].Box.UsedCBM(), p.Unplaced)
	}
	// Ask J's shipper to co-load; they decline -> co-loaded and priced, capped.
	j := p.Unplaced[0].Booking
	if err := a.RequestMove(ctx, ops, MoveInput{BookingID: j.ID, Target: "next_week", Reason: MoveReasons[0], AskShipper: true}); err != nil {
		t.Fatal(err)
	}
	jUser := userByEmail(t, a, "factory10@eimsky.test")
	mv := a.PendingMoveFor(ctx, j.ID)
	if mv == nil {
		t.Fatal("no pending move")
	}
	if err := a.RespondMove(ctx, jUser, mv.ID, false); err != nil {
		t.Fatal(err)
	}
	jb, _ := a.BookingFor(ctx, jUser, j.ID)
	if jb.Mode != "coload" || !jb.CapApplied || *jb.TotalCents != jb.CapCents {
		t.Fatalf("declined move %+v", jb)
	}
	// Vessel override, then lock.
	p, _ = a.Planner(ctx, planSailing.ID)
	for _, v := range p.Sailing.Vessels {
		if v.Service == "fast" {
			if err := a.ChooseVessel(ctx, ops, planSailing.ID, v.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	tids, err := a.LockAndTender(ctx, ops, planSailing.ID)
	if err != nil || len(tids) != 1 {
		t.Fatalf("lock %v %v", tids, err)
	}
	// Forwarders on the lane were invited and notified.
	open, _ = a.ForwarderTenders(ctx, fwB, "open")
	found := false
	for _, ft := range open {
		found = found || ft.ID == tids[0]
	}
	if !found {
		t.Fatal("forwarder not invited")
	}
	// Bookings can't be cancelled after lock.
	for _, l := range p.All {
		if l.Booking.CompanyID == fac.CompanyID && l.Booking.Mode == "pool" {
			if err := a.CancelBooking(ctx, fac, l.Booking.ID, "x"); err == nil {
				t.Fatal("cancel after lock")
			}
		}
	}

	// 6. Tender closes by job; award.
	a.Now = func() time.Time { return time.Now().Add(72 * time.Hour) }
	if n, err := a.CloseDueTenders(ctx); err != nil || n < 1 {
		t.Fatalf("close tenders %d %v", n, err)
	}
	a.Now = time.Now

	// 7. Insights hide nothing when enough factories, and never name one.
	in2, err := a.Insights(ctx, 8)
	if err != nil || in2.Suppressed || in2.InboundCBM <= 0 || in2.AvgFillPct <= 0 {
		t.Fatalf("insights %v %+v", err, in2)
	}

	// 8. Jobs tick without errors; emails delivered (logged).
	a.Tick(ctx)
	var pending int
	_ = a.DB.QueryRow(ctx, `SELECT count(*) FROM email_outbox WHERE sent_at IS NULL`).Scan(&pending)
	if pending != 0 {
		t.Fatalf("%d emails pending", pending)
	}
}

func TestAuthFlows(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()
	if _, err := a.Login(ctx, "factory@eimsky.test", "wrong"); err == nil {
		t.Fatal("bad password accepted")
	}
	u, err := a.SignupFactory(ctx, SignupInput{CompanyName: "Test Mills", Name: "Tess", Email: "Tess@Example.com", Password: "longenough"})
	if err != nil || u.CompanyStatus != "pending" || u.Email != "tess@example.com" {
		t.Fatalf("%v %+v", err, u)
	}
	if _, err := a.HoldBooking(ctx, u, BookingInput{}); err == nil {
		t.Fatal("pending factory booked")
	}
	if _, err := a.SignupFactory(ctx, SignupInput{CompanyName: "X", Name: "Y", Email: "tess@example.com", Password: "longenough"}); err == nil {
		t.Fatal("duplicate email")
	}
	// Reset link.
	if err := a.RequestPasswordReset(ctx, "tess@example.com"); err != nil {
		t.Fatal(err)
	}
	var body string
	_ = a.DB.QueryRow(ctx, `SELECT body FROM email_outbox WHERE to_addr='tess@example.com' ORDER BY id DESC LIMIT 1`).Scan(&body)
	i := strings.Index(body, "token=")
	tok := strings.Fields(body[i+6:])[0]
	if _, err := a.SetPasswordWithLink(ctx, "reset", tok, "brandnewpass"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetPasswordWithLink(ctx, "reset", tok, "again12345"); err == nil {
		t.Fatal("link reused")
	}
	if _, err := a.Login(ctx, "tess@example.com", "brandnewpass"); err != nil {
		t.Fatal(err)
	}
	// OTP: wrong code counts attempts.
	_, code, _ := a.RequestOTP(ctx, "+94 77 555 1234")
	if _, _, err := a.VerifyOTP(ctx, "0775551234", "000000"); err == nil && code != "000000" {
		t.Fatal("wrong code accepted")
	}
	nu, created, err := a.VerifyOTP(ctx, "0775551234", code)
	if err != nil || !created || nu.Phone != "+94775551234" {
		t.Fatalf("%v %v %+v", err, created, nu)
	}
	// New forwarder registers.
	nu, err = a.SaveForwarderProfile(ctx, nu, ForwarderProfile{CompanyName: "Fresh FF", ContactName: "Fay"})
	if err != nil || nu.CompanyID == 0 {
		t.Fatal(err)
	}
	if err := a.SubmitForwarder(ctx, nu); err == nil {
		t.Fatal("submitted without documents")
	}
	lanes, _ := a.Lanes(ctx, true)
	if err := a.SetForwarderLanes(ctx, nu, []int64{lanes[0].ID}); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"br_certificate", "ff_licence"} {
		if _, err := a.UploadDoc(ctx, nu, "company", nu.CompanyID, k, k+".pdf", demoPDF(k), "application/pdf"); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.SubmitForwarder(ctx, nu); err != nil {
		t.Fatal(err)
	}
	all := map[domain.Inclusion]bool{domain.IncOcean: true, domain.IncOrigin: true, domain.IncDestination: true, domain.IncDestuff: true}
	nu, _ = a.UserByID(ctx, nu.ID)
	if _, err := a.PlaceBid(ctx, nu, BidInput{TenderID: 1, AmountCents: 1, Includes: all}); err == nil {
		t.Fatal("pending forwarder bid")
	}
	admin := userByEmail(t, a, "admin@eimsky.test")
	if err := a.SetCompanyStatus(ctx, admin, nu.CompanyID, "approved", ""); err != nil {
		t.Fatal(err)
	}
	// Invite flow.
	_, link, err := a.InviteUser(ctx, admin, 0, RoleOps, "Olly", "olly@eimsky.test")
	if err != nil {
		t.Fatal(err)
	}
	tok = link[strings.Index(link, "token=")+6:]
	if _, err := a.SetPasswordWithLink(ctx, "invite", tok, "opsopsops"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Login(ctx, "olly@eimsky.test", "opsopsops"); err != nil {
		t.Fatal(err)
	}
}
