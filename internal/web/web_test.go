package web

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/itsmepava/container/internal/app"
	"github.com/itsmepava/container/internal/config"
	"github.com/itsmepava/container/internal/db"
	"github.com/itsmepava/container/internal/files"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func testServer(t *testing.T) (*httptest.Server, *app.App, *syncBuf) {
	t.Helper()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, dbURL)
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
	fs, _ := files.New(t.TempDir(), make([]byte, 32))
	logs := &syncBuf{}
	app.BcryptCost = 4
	a := app.New(pool, fs, config.Config{BaseURL: "http://test", DevMode: true}, slog.New(slog.NewTextHandler(io.MultiWriter(logs), nil)))
	if err := a.SeedDemo(ctx); err != nil {
		t.Fatal(err)
	}
	s, err := New(a)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, a, logs
}

type client struct {
	t    *testing.T
	base string
	c    *http.Client
	csrf string
}

func newClient(t *testing.T, base string) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: base, c: &http.Client{Jar: jar}}
}

var csrfRe = regexp.MustCompile(`name="csrf-token" content="([^"]+)"`)

func (c *client) get(path string, want int) string {
	c.t.Helper()
	resp, err := c.c.Get(c.base + path)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		c.t.Fatalf("GET %s: status %d, want %d\n%s", path, resp.StatusCode, want, string(b)[:min(len(b), 600)])
	}
	if m := csrfRe.FindSubmatch(b); m != nil {
		c.csrf = string(m[1])
	}
	return string(b)
}

func (c *client) post(path string, form url.Values) (string, string) {
	c.t.Helper()
	if form == nil {
		form = url.Values{}
	}
	if c.csrf != "" {
		form.Set("csrf", c.csrf)
	}
	resp, err := c.c.PostForm(c.base+path, form)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if m := csrfRe.FindSubmatch(b); m != nil {
		c.csrf = string(m[1])
	}
	return resp.Request.URL.Path, string(b)
}

func (c *client) login(email string) {
	c.t.Helper()
	c.get("/login", 200)
	path, body := c.post("/login", url.Values{"email": {email}, "password": {app.DemoPassword}})
	if path == "/login" {
		c.t.Fatalf("login %s failed: %s", email, body[:min(len(body), 400)])
	}
}

func TestPagesRender(t *testing.T) {
	ts, a, logs := testServer(t)
	ctx := context.Background()

	anon := newClient(t, ts.URL)
	for _, p := range []string{"/login", "/signup", "/forgot", "/reset?token=bad", "/healthz", "/static/css/app.css"} {
		anon.get(p, 200)
	}
	anon.get("/f/book", 200) // redirected to login
	anon.get("/nope", 404)

	fac := newClient(t, ts.URL)
	fac.login("factory@eimsky.test")
	body := fac.get("/f/book", 200)
	if !strings.Contains(body, "CBM remaining in 40HC") {
		t.Fatalf("booking sidebar missing: %s", body[len(body)-3000:])
	}
	bs, _ := a.CompanyBookings(ctx, 1, true)
	_ = bs
	fac.get("/f/bookings", 200)
	fac.get("/f/bookings?tab=past", 200)
	fac.get("/f/invoices", 200)
	fac.get("/f/team", 200)
	fac.get("/account", 200)
	fac.get("/notifications", 200)
	fac.get("/notifications/count", 200)
	fac.get("/hs?q=6006", 200)
	fac.get("/ops/pools", 404)

	// Quote fragment and a full booking through the web.
	u, _ := a.Login(ctx, "factory@eimsky.test", app.DemoPassword)
	cbs, _ := a.CompanyBookings(ctx, u.CompanyID, true)
	for _, b := range cbs {
		fac.get("/f/bookings/"+itoa(b.ID), 200)
		if b.Priced() {
			fac.get("/f/bookings/"+itoa(b.ID)+"/invoice.pdf", 200)
		}
	}
	var sha3 int64
	_ = a.DB.QueryRow(ctx, `SELECT s.id FROM sailings s JOIN lanes l ON l.id=s.lane_id WHERE l.origin='CNSHA' ORDER BY s.year DESC, s.week DESC LIMIT 1`).Scan(&sha3)
	form := url.Values{"sailing": {itoa(sha3)}, "from": {"CNSHA"}, "to": {"LKCMB"}, "category": {"fabric"}, "box": {"40HC"},
		"hs": {"6006.32", ""}, "pieces": {"10", ""}, "length": {"100", ""}, "width": {"50", ""}, "height": {"40", ""}, "kg": {"20", ""}}
	_, frag := fac.post("/f/book/quote", form)
	if !strings.Contains(frag, "Estimated cost: up to USD") || !strings.Contains(frag, "hx-swap-oob") {
		t.Fatalf("quote fragment: %s", frag[:min(len(frag), 500)])
	}
	path, page := fac.post("/f/book", form)
	if !strings.HasPrefix(path, "/f/bookings/") || !strings.Contains(page, "Confirm booking") {
		i := strings.Index(page, "alert bad")
		t.Fatalf("hold went to %s: %s", path, page[i:i+400])
	}
	path, page = fac.post(path+"/confirm", nil)
	if !strings.Contains(page, "Booked 2 CBM") {
		t.Fatalf("confirm: %s %s", path, page[:min(len(page), 300)])
	}

	ops := newClient(t, ts.URL)
	ops.login("ops@eimsky.test")
	for _, p := range []string{"/ops/pools", "/ops/tenders", "/ops/tenders?status=closed", "/ops/bookings", "/ops/bookings?status=unplaced&q=lotus", "/ops/invoices",
		"/admin/lanes", "/admin/lanes/new", "/admin/lanes/1", "/admin/sailings/new", "/admin/companies", "/admin/companies?status=pending", "/admin/users", "/admin/users?q=silva",
		"/admin/settings", "/admin/audit", "/insights", "/insights?weeks=26", "/dev/mail", "/account", "/notifications", "/ops/payables", "/ops/payables?status=due"} {
		ops.get(p, 200)
	}
	ops.get("/f/book", 404)
	ss, _ := a.Sailings(ctx, app.SailingFilter{})
	for _, s := range ss {
		ops.get("/ops/sailings/"+itoa(s.ID)+"/plan", 200)
		ops.get("/admin/sailings/"+itoa(s.ID), 200)
		ops.get("/ops/pools?year="+itoa(int64(s.Year))+"&week="+itoa(int64(s.Week))+"&s="+itoa(s.ID), 200)
	}
	ts2, _ := a.TenderList(ctx, "")
	for _, x := range ts2 {
		ops.get("/ops/tenders/"+itoa(x.ID), 200)
	}
	cos, _ := a.Companies(ctx, "", "", "")
	for _, c := range cos {
		ops.get("/admin/companies/"+itoa(c.ID), 200)
	}
	all, _ := a.SearchBookings(ctx, "", "", 0, 50)
	for _, b := range all {
		ops.get("/ops/bookings/"+itoa(b.ID), 200)
	}
	invs, _ := a.Invoices(ctx, 0, "")
	if len(invs) > 0 {
		ops.get("/ops/invoices/"+itoa(invs[0].ID)+".pdf", 200)
	}

	// Award the closed tender through the web.
	closed, _ := a.TenderList(ctx, "closed")
	v, _ := a.TenderView(ctx, closed[0].ID, 0)
	ops.get("/ops/tenders/"+itoa(closed[0].ID), 200)
	_, page = ops.post("/ops/tenders/"+itoa(closed[0].ID)+"/award", url.Values{"bid": {itoa(v.AutoPick.Bid.ID)}})
	if !strings.Contains(page, "Awarded. The winner") {
		t.Fatalf("award: %s", page[:min(len(page), 300)])
	}
	ops.get("/ops/tenders/"+itoa(closed[0].ID), 200)

	jaaf := newClient(t, ts.URL)
	jaaf.login("jaaf@eimsky.test")
	if b := jaaf.get("/insights", 200); !strings.Contains(b, "Inbound volume") {
		t.Fatal("insights empty")
	}
	jaaf.get("/ops/pools", 404)

	// CSRF: a post without the token is refused.
	bad := newClient(t, ts.URL)
	bad.login("ops@eimsky.test")
	bad.csrf = "wrong"
	_, page = bad.post("/admin/settings", url.Values{"fee_per_wm": {"99"}})
	if !strings.Contains(page, "session expired") {
		t.Fatal("csrf not enforced")
	}

	if l := logs.String(); strings.Contains(l, "render") || strings.Contains(l, "fragment") || strings.Contains(l, "level=ERROR") {
		t.Fatalf("errors logged:\n%s", grepErrors(l))
	}
}

func grepErrors(l string) string {
	var out []string
	for _, line := range strings.Split(l, "\n") {
		if strings.Contains(line, "ERROR") || strings.Contains(line, "render") {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func itoa(n int64) string { return strconvI(n) }
