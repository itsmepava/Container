package app

import (
	"context"
	"fmt"
	"time"

	"github.com/itsmepava/container/internal/domain"
	"github.com/jackc/pgx/v5"
)

// DemoPassword is the password of every seeded web account.
const DemoPassword = "eimsky-demo"

// SeedReference loads ports, container types, HS codes and default settings. Safe to rerun.
func (a *App) SeedReference(ctx context.Context) error {
	return a.Tx(ctx, func(tx pgx.Tx) error {
		ports := [][3]string{
			{"CNSHA", "Shanghai", "China"}, {"CNNGB", "Ningbo", "China"}, {"CNSZX", "Shenzhen", "China"}, {"CNTAO", "Qingdao", "China"},
			{"HKHKG", "Hong Kong", "Hong Kong"}, {"INMAA", "Chennai", "India"}, {"INTUT", "Tuticorin", "India"}, {"INNSA", "Nhava Sheva", "India"},
			{"BDCGP", "Chittagong", "Bangladesh"}, {"VNSGN", "Ho Chi Minh City", "Vietnam"}, {"LKCMB", "Colombo", "Sri Lanka"},
		}
		for _, p := range ports {
			if _, err := tx.Exec(ctx, `INSERT INTO ports(code,name,country) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, p[0], p[1], p[2]); err != nil {
				return err
			}
		}
		for i, s := range domain.DefaultContainerSpecs {
			if _, err := tx.Exec(ctx, `INSERT INTO container_types(code,name,internal_cbm,payload_kg,sort) VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, s.Code, s.Name, s.InternalCBM, s.PayloadKg, i); err != nil {
				return err
			}
		}
		for _, h := range hsSeed {
			if _, err := tx.Exec(ctx, `INSERT INTO hs_codes(code,description) VALUES ($1,$2) ON CONFLICT DO NOTHING`, h[0], h[1]); err != nil {
				return err
			}
		}
		for k, v := range settingDefaults {
			if _, err := tx.Exec(ctx, `INSERT INTO settings(key,value) VALUES ($1,$2) ON CONFLICT DO NOTHING`, k, v); err != nil {
				return err
			}
		}
		return nil
	})
}

// EnsureAdmin creates the first admin if there are no users yet.
func (a *App) EnsureAdmin(ctx context.Context, email, password string) error {
	var n int
	if err := a.DB.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil || n > 0 {
		return err
	}
	email, err := NormalizeEmail(email)
	if err != nil {
		return err
	}
	if err := ValidatePassword(password); err != nil {
		return err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	_, err = a.DB.Exec(ctx, `INSERT INTO users(role,name,email,password_hash) VALUES ('admin','Administrator',$1,$2)`, email, hash)
	return err
}

// minimal valid PDF used for seeded documents.
func demoPDF(title string) []byte {
	body := fmt.Sprintf("BT /F1 18 Tf 72 720 Td (%s) Tj ET", title)
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(body), body),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	out := "%PDF-1.4\n"
	var offs []int
	for i, o := range objs {
		offs = append(offs, len(out))
		out += fmt.Sprintf("%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := len(out)
	out += fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, o := range offs {
		out += fmt.Sprintf("%010d 00000 n \n", o)
	}
	out += fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return []byte(out)
}

type seedLot struct {
	cbm float64
	kg  float64
	cat domain.Category
	hs  string
}

// SeedDemo fills an empty database with a realistic demo, dated relative to now.
func (a *App) SeedDemo(ctx context.Context) error {
	if err := a.SeedReference(ctx); err != nil {
		return err
	}
	var n int
	_ = a.DB.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	if n > 0 {
		return fmt.Errorf("database already has users; run with --reset to reseed")
	}
	pw, err := HashPassword(DemoPassword)
	if err != nil {
		return err
	}
	q := a.DB
	company := func(kind, name, reg, status string) int64 {
		var id int64
		must(q.QueryRow(ctx, `INSERT INTO companies(kind,name,reg_number,address,status,submitted_at) VALUES ($1,$2,$3,$4,$5,now()) RETURNING id`, kind, name, reg, "Colombo, Sri Lanka", status).Scan(&id))
		return id
	}
	user := func(cid int64, role Role, name, email, phone string) int64 {
		var id int64
		var c any
		if cid != 0 {
			c = cid
		}
		hash := pw
		if role == RoleForwarder {
			hash = ""
		}
		must(q.QueryRow(ctx, `INSERT INTO users(company_id,role,name,email,phone,password_hash) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`, c, role, name, email, phone, hash).Scan(&id))
		return id
	}

	adminID := user(0, RoleAdmin, "Nadia Perera", "admin@eimsky.test", "")
	user(0, RoleOps, "Ravi Fernando", "ops@eimsky.test", "")
	jaaf := company("association", "Joint Apparel Association Forum", "", "approved")
	user(jaaf, RoleViewer, "JAAF Secretariat", "jaaf@eimsky.test", "")
	admin, _ := a.UserByID(ctx, adminID)

	names := []string{"Ceylon Weave Mills", "Kandy Knit Works", "Galle Trim House", "Negombo Apparel", "Lagoon Fabrics", "Palmyra Packaging",
		"Hill Country Accessories", "Southern Stitch", "Harbour Pack Co", "Lotus Textiles"}
	var factories []int64
	var factoryUsers []int64
	for i, nm := range names {
		cid := company("factory", nm, fmt.Sprintf("PV-%05d", 10231+i*17), "approved")
		factories = append(factories, cid)
		email := fmt.Sprintf("factory%d@eimsky.test", i+1)
		if i == 0 {
			email = "factory@eimsky.test"
		}
		factoryUsers = append(factoryUsers, user(cid, RoleFactoryAdmin, []string{"Amal Silva", "Dilani Jayasuriya", "Kasun Wickrama", "Shanika De Mel", "Ruwan Bandara", "Tharushi Peris", "Chamara Dias", "Ishara Gunasekara", "Nimal Rajapakse", "Sachini Herath"}[i], email, fmt.Sprintf("+9477100%04d", i+1)))
	}
	user(factories[0], RoleFactoryStaff, "Pradeep Kumara", "staff@eimsky.test", "")
	pendingFactory := company("factory", "Matara Garments", "PV-20001", "pending")
	user(pendingFactory, RoleFactoryAdmin, "Hasini Abeysekera", "matara@eimsky.test", "")

	fwNames := []string{"Blue Harbour Logistics", "Serendib Freight", "Lionport Shipping", "Monsoon Cargo", "Spice Route Forwarding", "Pearl Line Logistics"}
	var fws []int64
	for i, nm := range fwNames {
		cid := company("forwarder", nm, fmt.Sprintf("FF-%04d", 311+i), "approved")
		fws = append(fws, cid)
		user(cid, RoleForwarder, []string{"Asanka Mendis", "Dinesh Ratnayake", "Gayan Fonseka", "Lakmal Senanayake", "Roshan Weerasinghe", "Upeksha Karunaratne"}[i], fmt.Sprintf("forwarder%d@eimsky.test", i+1), fmt.Sprintf("+9477000%04d", i+1))
	}
	pendingFw := company("forwarder", "New Wave Forwarders", "FF-0999", "pending")
	user(pendingFw, RoleForwarder, "Sunil Jayawardena", "", "+94770000009")

	// Lanes.
	lane := func(o, d string, cfs string, box string, rates map[string]float64, coload, est float64) int64 {
		id, err := a.SaveLane(ctx, admin, LaneInput{Origin: o, Dest: d, Active: true, ColoadRate: coload, EstimatePerWM: est, OriginCFS: cfs, DefaultBox: box, DefaultGrade: "A", Rates: rates})
		must(err)
		return id
	}
	sha := lane("CNSHA", "LKCMB", "Waigaoqiao CFS, Shanghai", "40HC", map[string]float64{"20GP": 1450, "40GP": 2200, "40HC": 2350, "45HC": 2600}, 55, 50)
	ngb := lane("CNNGB", "LKCMB", "Beilun CFS, Ningbo", "40HC", map[string]float64{"20GP": 1400, "40GP": 2150, "40HC": 2300, "45HC": 2550}, 54, 50)
	maa := lane("INMAA", "LKCMB", "Chennai Port CFS", "20GP", map[string]float64{"20GP": 900, "40GP": 1350, "40HC": 1450}, 48, 46)
	for i, f := range fws {
		lanes := []int64{sha, ngb}
		if i >= 2 {
			lanes = append(lanes, maa)
		}
		for _, l := range lanes {
			_, _ = q.Exec(ctx, `INSERT INTO forwarder_lanes(company_id,lane_id) VALUES ($1,$2)`, f, l)
		}
	}
	_, _ = q.Exec(ctx, `INSERT INTO forwarder_lanes(company_id,lane_id) VALUES ($1,$2)`, pendingFw, sha)

	now := a.Now()
	cy, cw := domain.ISOWeek(now)
	weekOf := func(off int) (int, int, time.Time) {
		m := domain.WeekStart(cy, cw).AddDate(0, 0, 7*off)
		y, w := domain.ISOWeek(m)
		return y, w, m
	}
	at := func(t time.Time, h, m int) time.Time {
		return time.Date(t.Year(), t.Month(), t.Day(), h, m, 0, 0, domain.Colombo)
	}
	// sailing creates week `off` (relative to now) on a lane. The CFS cut-off is
	// Monday 17:00 of that week; plan lock is the Saturday before, bids close Sunday.
	sailing := func(laneID int64, off int, voyageA, voyageB string, transA, transB int) int64 {
		y, w, mon := weekOf(off)
		cut := at(mon, 17, 0)
		in := SailingInput{LaneID: laneID, Year: y, Week: w, CFSCutoff: cut, PlanLockAt: at(mon.AddDate(0, 0, -2), 17, 0), BidCloseAt: at(mon.AddDate(0, 0, -1), 17, 0),
			Vessels: []VesselInput{
				{Name: "MV Kelani Star", Voyage: voyageA, Service: "standard", Via: "Port Klang", ETD: at(mon.AddDate(0, 0, 3), 18, 0), ETA: at(mon.AddDate(0, 0, 3+transA), 8, 0), IsDefault: true},
				{Name: "MV Indigo Bay", Voyage: voyageB, Service: "fast", ETD: at(mon.AddDate(0, 0, 2), 20, 0), ETA: at(mon.AddDate(0, 0, 2+transB), 8, 0), ExtraPerBox: 150},
			}}
		id, err := a.CreateSailing(ctx, admin, in)
		must(err)
		return id
	}
	lotsW := []seedLot{
		{6.6, 2016, domain.CatFabric, "6006.32"}, {9.2, 2100, domain.CatFabric, "5208.42"}, {7.4, 1500, domain.CatTrims, "9606.21"},
		{5.8, 1300, domain.CatFabric, "5407.61"}, {8.1, 1700, domain.CatFabric, "6004.10"}, {6.0, 1100, domain.CatPackaging, "4819.10"},
		{4.9, 800, domain.CatTrims, "9607.11"}, {5.5, 1000, domain.CatFabric, "5513.21"}, {4.5, 884, domain.CatPackaging, "3923.21"},
		{8.2, 1900, domain.CatFabric, "5209.42"},
	}
	// booking inserts a confirmed booking with one cargo line (two for Factory A's worked example).
	seq := 300
	booking := func(sailingID int64, fi int, l seedLot, containerID int64, confirmed time.Time, week int) int64 {
		var cut time.Time
		_ = q.QueryRow(ctx, `SELECT s.cfs_cutoff FROM sailings s WHERE s.id=$1`, sailingID).Scan(&cut)
		var est float64
		_ = q.QueryRow(ctx, `SELECT l.estimate_per_wm FROM sailings s JOIN lanes l ON l.id=s.lane_id WHERE s.id=$1`, sailingID).Scan(&est)
		var box string
		_ = q.QueryRow(ctx, `SELECT box_code FROM containers WHERE sailing_id=$1 ORDER BY seq LIMIT 1`, sailingID).Scan(&box)
		lines := []domain.CargoLine{}
		if fi == 0 && l.cbm == 6.6 {
			lines = []domain.CargoLine{
				{HSCode: "6006.32", Pieces: 40, LengthCm: 160, WidthCm: 30, HeightCm: 30, KgEach: 45},
				{HSCode: "9606.21", Pieces: 12, LengthCm: 50, WidthCm: 40, HeightCm: 35, KgEach: 18},
			}
		} else {
			// One line of cartons sized to the lot's volume.
			pieces := int(l.cbm/0.1 + 0.5)
			lines = []domain.CargoLine{{HSCode: l.hs, Pieces: pieces, LengthCm: 50, WidthCm: 40, HeightCm: 50 * l.cbm / (float64(pieces) * 0.1), KgEach: domain.Round(l.kg/float64(pieces), 2)}}
		}
		p, cbm, kg := domain.Totals(lines)
		wm := domain.WM(cbm, kg)
		seq++
		var id int64
		var cont any
		if containerID != 0 {
			cont = containerID
		}
		must(q.QueryRow(ctx, `INSERT INTO bookings(ref,company_id,created_by,sailing_id,box_code,category,pieces,cbm,kg,wm,cap_cents,status,container_id,confirmed_at,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'booked',$12,$13,$13) RETURNING id`,
			fmt.Sprintf("EIM-%02d-%04d", week, seq), factories[fi], factoryUsers[fi], sailingID, box, l.cat, p, cbm, kg, wm, domain.Cents(est*wm), cont, confirmed).Scan(&id))
		for i, ln := range lines {
			_, err := q.Exec(ctx, `INSERT INTO booking_lines(booking_id,line_no,hs_code,pieces,length_cm,width_cm,height_cm,kg_each,cbm,kg) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
				id, i+1, ln.HSCode, ln.Pieces, ln.LengthCm, ln.WidthCm, domain.Round(ln.HeightCm, 1), ln.KgEach, ln.CBM(), ln.Kg())
			must(err)
		}
		return id
	}
	firstContainer := func(sid int64) int64 {
		var id int64
		must(q.QueryRow(ctx, `SELECT id FROM containers WHERE sailing_id=$1 ORDER BY seq LIMIT 1`, sid).Scan(&id))
		return id
	}
	upload := func(ownerKind string, ownerID int64, kind, name string) {
		_, err := a.UploadDoc(ctx, admin, ownerKind, ownerID, kind, name, demoPDF(name), "application/pdf")
		must(err)
	}
	bid := func(tid int64, fi int, usd float64, vesselID int64, at time.Time) {
		var uid int64
		must(q.QueryRow(ctx, `SELECT id FROM users WHERE company_id=$1 LIMIT 1`, fws[fi]).Scan(&uid))
		_, err := q.Exec(ctx, `INSERT INTO bids(tender_id,company_id,user_id,amount_cents,vessel_id,inc_ocean,inc_origin,inc_destination,inc_destuff,submitted_at,updated_at) VALUES ($1,$2,$3,$4,$5,true,true,true,true,$6,$6)`,
			tid, fws[fi], uid, domain.Cents(usd), vesselID, at)
		must(err)
	}
	tender := func(sid, cid int64, vesselID int64, closes time.Time, week int, invite []int) int64 {
		var s int64
		_ = q.QueryRow(ctx, `SELECT nextval('tender_ref_seq')`).Scan(&s)
		var tid int64
		must(q.QueryRow(ctx, `INSERT INTO tenders(ref,container_id,vessel_id,opens_at,closes_at) VALUES ($1,$2,$3,$4,$5) RETURNING id`,
			fmt.Sprintf("TND-%02d-%04d", week, s), cid, vesselID, closes.Add(-40*time.Hour), closes).Scan(&tid))
		_, _ = q.Exec(ctx, `UPDATE containers SET status='tendering' WHERE id=$1`, cid)
		_, _ = q.Exec(ctx, `UPDATE sailings SET status='tendering', decision='own' WHERE id=$1`, sid)
		for _, f := range invite {
			_, _ = q.Exec(ctx, `INSERT INTO tender_invites(tender_id,company_id) VALUES ($1,$2)`, tid, fws[f])
		}
		return tid
	}
	vessels := func(sid int64) (std, fast int64) {
		_ = q.QueryRow(ctx, `SELECT id FROM vessel_options WHERE sailing_id=$1 AND service='standard'`, sid).Scan(&std)
		_ = q.QueryRow(ctx, `SELECT id FROM vessel_options WHERE sailing_id=$1 AND service='fast'`, sid).Scan(&fast)
		return
	}
	allFw := []int{0, 1, 2, 3, 4, 5}

	// --- History: Shanghai weeks -4..-2 and Ningbo week -1, all awarded. ---
	for off := -4; off <= -1; off++ {
		laneID := sha
		if off == -1 {
			laneID = ngb
		}
		sid := sailing(laneID, off, fmt.Sprintf("%03dW", 44+off+4), fmt.Sprintf("%03dE", 43+off+4), 9, 6)
		_, w, _ := weekOf(off)
		cid := firstContainer(sid)
		var bks []int64
		count := 8 + (off+4)%2
		for i := 0; i < count; i++ {
			l := lotsW[(i+off+8)%9]
			bks = append(bks, booking(sid, i, l, cid, now.AddDate(0, 0, 7*off-6), w))
		}
		std, fast := vessels(sid)
		_, _ = q.Exec(ctx, `UPDATE sailings SET chosen_vessel_id=$2 WHERE id=$1`, sid, fast)
		var lock time.Time
		_ = q.QueryRow(ctx, `SELECT bid_close_at FROM sailings WHERE id=$1`, sid).Scan(&lock)
		tid := tender(sid, cid, fast, lock, w, allFw)
		bid(tid, 0, 2280-float64((off+4)*20), fast, lock.Add(-20*time.Hour))
		bid(tid, 1, 2340, fast, lock.Add(-10*time.Hour))
		bid(tid, 2, 2410, fast, lock.Add(-5*time.Hour))
		bid(tid, 3, 2200, std, lock.Add(-3*time.Hour))
		_, _ = q.Exec(ctx, `UPDATE tenders SET status='closed' WHERE id=$1`, tid)
		var win int64
		_ = q.QueryRow(ctx, `SELECT id FROM bids WHERE tender_id=$1 AND company_id=$2`, tid, fws[0]).Scan(&win)
		must(a.Award(ctx, admin, tid, win, ""))
		upload("tender", tid, "booking_confirmation", fmt.Sprintf("BKG-CONF-%s.pdf", fmt.Sprint(tid)))
		for i, b := range bks {
			if off == -1 && i == 1 {
				continue // one shipper still owes its invoice
			}
			upload("booking", b, "commercial_invoice", fmt.Sprintf("INV-SH-%04d.pdf", 2200+int(b)))
			if i%3 == 0 {
				upload("booking", b, "import_licence", fmt.Sprintf("LIC-%04d.pdf", 500+int(b)))
			}
		}
		if off < -1 {
			// Fabric lots need licences too; complete the older weeks.
			for _, b := range bks {
				upload("booking", b, "import_licence", fmt.Sprintf("LIC-%04d.pdf", 500+int(b)))
			}
		}
	}
	if _, err := a.IssueDueInvoices(ctx); err != nil {
		return err
	}
	_, _ = q.Exec(ctx, `UPDATE invoices SET status='paid', paid_at=issued_at + interval '5 days' WHERE issued_at < now() - interval '1 second' AND booking_id IN (
		SELECT b.id FROM bookings b JOIN sailings s ON s.id=b.sailing_id WHERE s.cfs_cutoff < $1)`, now.AddDate(0, 0, -10))

	// --- This week's Shanghai sailing (week +1): locked, 9 lots, bids closed, awaiting award. ---
	shaW := sailing(sha, 1, "049W", "048E", 9, 6)
	_, w1, _ := weekOf(1)
	cid := firstContainer(shaW)
	stdW, fastW := vessels(shaW)
	var shaWBookings []int64
	for i := 0; i < 9; i++ {
		shaWBookings = append(shaWBookings, booking(shaW, i, lotsW[i], cid, now.Add(-time.Duration(80-i*6)*time.Hour), w1))
	}
	// Factory J was moved to next week (accepted) — recorded below once week +2 exists.
	_, _ = q.Exec(ctx, `UPDATE sailings SET chosen_vessel_id=$2, plan_lock_at=$3, bid_close_at=$4 WHERE id=$1`, shaW, fastW, now.Add(-26*time.Hour), now.Add(-2*time.Hour))
	_ = a.Audit(ctx, q, admin, "plan.vessel", "sailing", shaW, "Vessel overridden to MV Indigo Bay · 048E, fast, direct", nil)
	tW := tender(shaW, cid, fastW, now.Add(-2*time.Hour), w1, []int{0, 1, 2, 3, 4, 5})
	_ = a.Audit(ctx, q, admin, "tender.open", "tender", tW, "Plan locked, tender opened to 6 forwarders", nil)
	bid(tW, 3, 2250, stdW, now.Add(-20*time.Hour))
	bid(tW, 0, 2350, fastW, now.Add(-14*time.Hour))
	bid(tW, 1, 2410, fastW, now.Add(-9*time.Hour))
	bid(tW, 2, 2480, fastW, now.Add(-4*time.Hour))
	if _, err := a.CloseDueTenders(ctx); err != nil {
		return err
	}
	for i, b := range shaWBookings {
		if i == 1 {
			continue // Factory B: invoice missing
		}
		upload("booking", b, "commercial_invoice", fmt.Sprintf("INV-SH-%04d.pdf", 2291+i))
		if lotsW[i].cat == domain.CatFabric {
			upload("booking", b, "import_licence", fmt.Sprintf("LIC-%04d.pdf", 900+i))
		}
	}

	// --- Ningbo week +1: open tender, closes in about 30 hours. ---
	ngbW := sailing(ngb, 1, "112W", "087E", 10, 7)
	cidN := firstContainer(ngbW)
	for i, l := range []seedLot{{6.1, 1300, domain.CatFabric, "5208.52"}, {7.0, 1500, domain.CatFabric, "6006.22"}, {5.2, 900, domain.CatTrims, "5807.10"}, {4.8, 700, domain.CatPackaging, "4819.10"}, {7.9, 1700, domain.CatFabric, "5407.52"}} {
		booking(ngbW, i+2, l, cidN, now.Add(-time.Duration(70-i*5)*time.Hour), w1)
	}
	stdN, _ := vessels(ngbW)
	_, _ = q.Exec(ctx, `UPDATE sailings SET plan_lock_at=$2, bid_close_at=$3 WHERE id=$1`, ngbW, now.Add(-10*time.Hour), now.Add(30*time.Hour))
	tN := tender(ngbW, cidN, stdN, now.Add(30*time.Hour), w1, allFw)
	_ = a.Audit(ctx, q, admin, "tender.open", "tender", tN, "Plan locked, tender opened to 6 forwarders", nil)
	bid(tN, 1, 1990, stdN, now.Add(-3*time.Hour))
	bid(tN, 2, 2060, stdN, now.Add(-2*time.Hour))

	// --- Chennai week +1: a small 20GP pool, below break-even. ---
	maaW := sailing(maa, 1, "021S", "009S", 3, 2)
	cidM := firstContainer(maaW)
	for i, l := range []seedLot{{3.2, 700, domain.CatTrims, "9607.19"}, {2.9, 600, domain.CatPackaging, "4821.10"}, {2.9, 650, domain.CatFabric, "5208.12"}} {
		booking(maaW, i+5, l, cidM, now.Add(-time.Duration(30-i*5)*time.Hour), w1)
	}

	// --- Shanghai week +2: the load planner demo, ten requests with one overflow. ---
	shaN := sailing(sha, 2, "050W", "049E", 9, 6)
	_, w2, _ := weekOf(2)
	cidS := firstContainer(shaN)
	var nextBookings []int64
	for i := 0; i < 10; i++ {
		cont := cidS
		if i == 9 {
			cont = 0
		}
		nextBookings = append(nextBookings, booking(shaN, i, lotsW[i], cont, now.Add(-time.Duration(48-i*4)*time.Hour), w2))
	}
	for i, b := range nextBookings[:8] {
		upload("booking", b, "commercial_invoice", fmt.Sprintf("INV-SH-%04d.pdf", 3100+i))
		if lotsW[i].cat == domain.CatFabric && i != 3 {
			upload("booking", b, "import_licence", fmt.Sprintf("LIC-%04d.pdf", 3200+i))
		}
	}
	_ = a.Audit(ctx, q, nil, "move.accepted", "booking", nextBookings[9], "Lotus Textiles accepted the move of its 8.2 CBM from Week "+fmt.Sprint(w1)+" to Week "+fmt.Sprint(w2), nil)

	// --- Shanghai week +3: just opened. ---
	sha3 := sailing(sha, 3, "051W", "050E", 9, 6)
	_, w3, _ := weekOf(3)
	for i, l := range []seedLot{{3.5, 800, domain.CatFabric, "5208.42"}, {2.6, 500, domain.CatTrims, "9606.22"}, {3.1, 700, domain.CatFabric, "6006.32"}, {2.8, 400, domain.CatPackaging, "4819.20"}} {
		booking(sha3, i+3, l, firstContainer(sha3), now.Add(-time.Duration(10-i*2)*time.Hour), w3)
	}

	// Forwarder documents for the pending registration.
	upload("company", pendingFw, "br_certificate", "BR-certificate.pdf")
	upload("company", pendingFw, "ff_licence", "FF-licence.pdf")
	for _, f := range fws {
		upload("company", f, "br_certificate", "BR-certificate.pdf")
		upload("company", f, "ff_licence", "FF-licence.pdf")
	}

	// Seeded history shouldn't fire emails and pushes.
	_, _ = q.Exec(ctx, `UPDATE email_outbox SET sent_at=now()`)
	_, _ = q.Exec(ctx, `UPDATE push_outbox SET sent_at=now()`)
	_, _ = q.Exec(ctx, `UPDATE notifications SET read_at=now() WHERE title NOT LIKE 'Bids closed on%' AND title NOT LIKE 'New forwarder to approve%'`)
	return nil
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

var hsSeed = [][2]string{
	{"3204.16", "Reactive dyes"}, {"3204.12", "Acid and mordant dyes"}, {"3809.91", "Textile finishing agents"},
	{"3923.21", "Polyethylene bags and sacks"}, {"3923.29", "Plastic bags, other plastics"}, {"3926.20", "Plastic garment accessories"},
	{"4819.10", "Corrugated cartons and boxes"}, {"4819.20", "Folding cartons, non-corrugated"}, {"4821.10", "Printed paper labels and tags"},
	{"4823.90", "Paper tissue and interleaving"}, {"5204.11", "Cotton sewing thread"}, {"5208.12", "Woven cotton, plain weave, unbleached"},
	{"5208.42", "Woven cotton, plain weave, dyed yarns"}, {"5208.52", "Woven cotton, plain weave, printed"}, {"5209.42", "Denim"},
	{"5212.24", "Woven cotton blends, dyed yarns"}, {"5401.10", "Synthetic sewing thread"}, {"5407.52", "Woven polyester filament, dyed"},
	{"5407.61", "Woven polyester filament, non-textured"}, {"5513.21", "Woven polyester/cotton blend, dyed"}, {"5515.12", "Woven polyester/viscose blend"},
	{"5603.12", "Non-wovens (interlinings)"}, {"5604.10", "Elastic thread and cord"}, {"5806.20", "Narrow elastic tape"},
	{"5806.32", "Narrow woven fabric, man-made fibres"}, {"5807.10", "Woven labels and badges"}, {"5807.90", "Printed and other labels"},
	{"5808.10", "Braids in the piece"}, {"5811.00", "Quilted textile products"}, {"6001.22", "Knitted pile fabric, man-made"},
	{"6002.40", "Knitted fabric with elastomeric yarn"}, {"6004.10", "Knitted elastic fabric, over 30 cm"}, {"6005.36", "Warp-knit fabric, synthetic, dyed"},
	{"6006.22", "Knitted cotton fabric, dyed"}, {"6006.32", "Knitted synthetic fabric, dyed"}, {"6006.34", "Knitted synthetic fabric, printed"},
	{"6217.10", "Garment accessories (pads, shoulder pads)"}, {"7318.15", "Steel screws and bolts"}, {"8308.10", "Hooks, eyes and eyelets"},
	{"8308.90", "Buckles and clasps"}, {"9606.21", "Plastic buttons, not covered"}, {"9606.22", "Metal buttons, not covered"},
	{"9606.29", "Other buttons"}, {"9606.30", "Button moulds and blanks"}, {"9607.11", "Zip fasteners, metal teeth"},
	{"9607.19", "Zip fasteners, other"}, {"9607.20", "Zip parts and sliders"},
}
