package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/itsmepava/container/internal/app"
	"github.com/itsmepava/container/internal/config"
	"github.com/itsmepava/container/internal/db"
	"github.com/itsmepava/container/internal/files"
)

func setup(t *testing.T) (*httptest.Server, *app.App) {
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
	_ = db.Reset(ctx, pool)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	fs, _ := files.New(t.TempDir(), make([]byte, 32))
	app.BcryptCost = 4
	a := app.New(pool, fs, config.Config{BaseURL: "http://t", DevMode: true}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := a.SeedDemo(ctx); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", New(a).Handler()))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, a
}

type cli struct {
	t     *testing.T
	base  string
	token string
}

func (c *cli) do(method, path string, body any, want int, out any) {
	c.t.Helper()
	var rd io.Reader
	ct := "application/json"
	switch b := body.(type) {
	case nil:
	case *bytes.Buffer:
		rd = b
	case [2]any: // multipart: buffer, content type
		rd, ct = b[0].(*bytes.Buffer), b[1].(string)
	default:
		bs, _ := json.Marshal(b)
		rd = bytes.NewReader(bs)
	}
	req, _ := http.NewRequest(method, c.base+"/api/v1"+path, rd)
	req.Header.Set("Content-Type", ct)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		c.t.Fatalf("%s %s: %d want %d: %s", method, path, resp.StatusCode, want, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			c.t.Fatalf("%s: %v %s", path, err, raw)
		}
	}
}

func (c *cli) login(phone string) {
	var o struct{ Phone, DevCode string }
	c.do("POST", "/auth/otp", map[string]string{"phone": phone}, 200, &o)
	var v struct {
		Token string
		User  userJSON
	}
	c.do("POST", "/auth/verify", map[string]string{"phone": phone, "code": "000000x"}, 400, nil)
	c.do("POST", "/auth/verify", map[string]string{"phone": phone, "code": o.DevCode}, 200, &v)
	c.token = v.Token
}

func upload(kind string) [2]any {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("kind", kind)
	f, _ := w.CreateFormFile("file", kind+".pdf")
	f.Write([]byte("%PDF-1.4 test"))
	w.Close()
	return [2]any{&buf, w.FormDataContentType()}
}

func TestForwarderFlow(t *testing.T) {
	ts, a := setup(t)
	ctx := context.Background()
	c := &cli{t: t, base: ts.URL}
	c.do("GET", "/me", nil, 401, nil)
	c.do("GET", "/meta", nil, 200, nil)
	c.login("+94770000002") // Serendib Freight

	var me map[string]any
	c.do("GET", "/me", nil, 200, &me)
	if me["user"].(map[string]any)["companyStatus"] != "approved" {
		t.Fatal(me)
	}
	var open []tenderJSON
	c.do("GET", "/tenders?scope=open", nil, 200, &open)
	if len(open) != 1 || open[0].MyBid == nil {
		t.Fatalf("open %+v", open)
	}
	var tj tenderJSON
	c.do("GET", "/tenders/"+itoa(open[0].ID), nil, 200, &tj)
	if len(tj.Vessels) != 2 {
		t.Fatal("vessels")
	}
	all := map[string]bool{"ocean": true, "origin": true, "destination": true, "destuff": true}
	c.do("PUT", "/tenders/"+itoa(tj.ID)+"/bid", map[string]any{"amount": 0}, 400, nil)
	c.do("PUT", "/tenders/"+itoa(tj.ID)+"/bid", map[string]any{"amount": 1950.5, "includes": all, "note": "Weekly service"}, 200, &tj)
	if tj.MyBid.Amount != 1950.5 || tj.MyBid.Note != "Weekly service" {
		t.Fatalf("%+v", tj.MyBid)
	}
	var mine []tenderJSON
	c.do("GET", "/tenders?scope=mine", nil, 200, &mine)
	if len(mine) < 2 {
		t.Fatalf("mine %d", len(mine))
	}

	// Award the closed Shanghai tender to Blue Harbour; it then sees shippers and uploads confirmation.
	closed, _ := a.TenderList(ctx, "closed")
	v, _ := a.TenderView(ctx, closed[0].ID, 0)
	ops, _ := a.Login(ctx, "ops@eimsky.test", app.DemoPassword)
	if err := a.Award(ctx, ops, closed[0].ID, v.AutoPick.Bid.ID, ""); err != nil {
		t.Fatal(err)
	}
	c.do("GET", "/tenders/"+itoa(closed[0].ID)+"/award", nil, 404, nil) // Serendib lost
	w := &cli{t: t, base: ts.URL}
	w.login("0770000001")
	var aw struct {
		Shippers []map[string]any
	}
	w.do("GET", "/tenders/"+itoa(closed[0].ID)+"/award", nil, 200, &aw)
	if len(aw.Shippers) != 9 || aw.Shippers[0]["email"] == "" {
		t.Fatalf("%+v", aw)
	}
	docs := aw.Shippers[0]["documents"].([]any)
	w.do("GET", "/documents/"+itoa(int64(docs[0].(map[string]any)["id"].(float64))), nil, 200, nil)
	c.do("GET", "/documents/"+itoa(int64(docs[0].(map[string]any)["id"].(float64))), nil, 404, nil)
	w.do("POST", "/tenders/"+itoa(closed[0].ID)+"/confirmation", upload("booking_confirmation"), 200, nil)
	c.do("POST", "/tenders/"+itoa(closed[0].ID)+"/confirmation", upload("booking_confirmation"), 400, nil)
	var won []tenderJSON
	w.do("GET", "/tenders?scope=awarded", nil, 200, &won)
	if len(won) == 0 || won[0].Result != "won" {
		t.Fatalf("%+v", won)
	}

	// A brand-new forwarder registers.
	n := &cli{t: t, base: ts.URL}
	n.login("0779998888")
	n.do("GET", "/tenders", nil, 403, nil)
	n.do("POST", "/me/submit", nil, 400, nil)
	n.do("PUT", "/me/profile", map[string]string{"companyName": "Kandy Freight", "contactName": "Kamal", "email": "kamal@kf.test"}, 200, nil)
	var lanes []map[string]any
	n.do("GET", "/lanes", nil, 200, &lanes)
	n.do("PUT", "/me/lanes", map[string]any{"laneIds": []int64{int64(lanes[0]["id"].(float64))}}, 200, nil)
	n.do("POST", "/me/documents", upload("br_certificate"), 200, nil)
	n.do("POST", "/me/documents", upload("ff_licence"), 200, nil)
	n.do("POST", "/me/submit", nil, 200, nil)
	n.do("GET", "/me", nil, 200, &me)
	if me["registration"].(map[string]any)["submitted"] != true {
		t.Fatal(me)
	}
	n.do("POST", "/push-token", map[string]string{"token": "ExponentPushToken[abc]", "platform": "ios"}, 200, nil)
	n.do("GET", "/notifications", nil, 200, nil)
	n.do("POST", "/auth/logout", nil, 200, nil)
	n.do("GET", "/me", nil, 401, nil)
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }
