// Package api is the JSON API used by the forwarder mobile app. All routes
// live under /api/v1 and authenticate with a bearer token from phone OTP.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/itsmepava/container/internal/app"
	"github.com/itsmepava/container/internal/domain"
	"github.com/itsmepava/container/internal/files"
)

type API struct {
	app *app.App
	mu  sync.Mutex
	hit map[string][]time.Time
}

func New(a *app.App) *API { return &API{app: a, hit: map[string][]time.Time{}} }

type ctxKey int

const keyUser ctxKey = 0

func userOf(r *http.Request) *app.User { u, _ := r.Context().Value(keyUser).(*app.User); return u }

func (x *API) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(cors)
	r.Route("/v1", func(r chi.Router) {
		r.Get("/meta", x.meta)
		r.Post("/auth/otp", x.otp)
		r.Post("/auth/verify", x.verify)
		r.Get("/files/{id}", x.signedFile)
		r.Group(func(r chi.Router) {
			r.Use(x.auth)
			r.Post("/auth/logout", x.logout)
			r.Get("/me", x.me)
			r.Put("/me/profile", x.profile)
			r.Get("/lanes", x.lanes)
			r.Put("/me/lanes", x.setLanes)
			r.Get("/me/documents", x.myDocs)
			r.Post("/me/documents", x.uploadCompanyDoc)
			r.Post("/me/submit", x.submit)
			r.Post("/push-token", x.pushToken)
			r.Delete("/push-token", x.deletePushToken)
			r.Get("/notifications", x.notifications)
			r.Post("/notifications/read", x.readNotifications)
			r.Get("/documents/{id}", x.document)
			r.Get("/documents/{id}/link", x.documentLink)
			r.Group(func(r chi.Router) {
				r.Use(approved)
				r.Get("/tenders", x.tenders)
				r.Get("/tenders/{id}", x.tender)
				r.Put("/tenders/{id}/bid", x.bid)
				r.Delete("/tenders/{id}/bid", x.withdraw)
				r.Get("/tenders/{id}/award", x.award)
				r.Post("/tenders/{id}/confirmation", x.confirmation)
			})
		})
	})
	r.NotFound(func(w http.ResponseWriter, r *http.Request) { fail(w, http.StatusNotFound, "Not found.") })
	return r
}

// ---------- plumbing ----------

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (x *API) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		sess, err := x.app.SessionByToken(r.Context(), tok, "api")
		if err != nil || !sess.User.IsForwarder() {
			fail(w, http.StatusUnauthorized, "Please sign in again.")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), keyUser, sess.User)))
	})
}

func approved(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u := userOf(r); u.CompanyID == 0 || u.CompanyStatus != "approved" {
			fail(w, http.StatusForbidden, "Your company is waiting for Eimsky's approval.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (x *API) allow(key string, n int, win time.Duration) bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	now := time.Now()
	var keep []time.Time
	for _, t := range x.hit[key] {
		if now.Sub(t) < win {
			keep = append(keep, t)
		}
	}
	if len(keep) >= n {
		x.hit[key] = keep
		return false
	}
	x.hit[key] = append(keep, now)
	return true
}

func ip(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

func ok(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (x *API) err(w http.ResponseWriter, err error) {
	if errors.Is(err, app.ErrNotFound) {
		fail(w, http.StatusNotFound, app.ErrNotFound.Msg)
		return
	}
	if errors.Is(err, app.ErrForbidden) {
		fail(w, http.StatusForbidden, app.ErrForbidden.Msg)
		return
	}
	if msg, isUser := app.IsUserError(err); isUser {
		fail(w, http.StatusBadRequest, msg)
		return
	}
	x.app.Log.Error("api", "err", err)
	fail(w, http.StatusInternalServerError, "Something went wrong on our side. Please try again.")
}

func decode(r *http.Request, v any) error {
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(v); err != nil {
		return app.Errf("We couldn't read that request.")
	}
	return nil
}

func idParam(r *http.Request) int64 {
	v, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	return v
}

// ---------- JSON shapes ----------

type userJSON struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Phone         string `json:"phone"`
	Email         string `json:"email"`
	CompanyID     int64  `json:"companyId"`
	CompanyName   string `json:"companyName"`
	CompanyStatus string `json:"companyStatus"`
}

func toUser(u *app.User) userJSON {
	st := u.CompanyStatus
	if u.CompanyID == 0 {
		st = "none"
	}
	return userJSON{u.ID, u.Name, u.Phone, u.Email, u.CompanyID, u.CompanyName, st}
}

type vesselJSON struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Voyage      string    `json:"voyage"`
	Label       string    `json:"label"`
	Service     string    `json:"service"`
	ServiceText string    `json:"serviceText"`
	ETD         time.Time `json:"etd"`
	ETA         time.Time `json:"eta"`
	TransitDays int       `json:"transitDays"`
	Via         string    `json:"via"`
}

func toVessel(v *app.VesselOption) *vesselJSON {
	if v == nil {
		return nil
	}
	return &vesselJSON{v.ID, v.Name, v.Voyage, v.Label(), v.Service, v.ServiceLabel(), v.ETD, v.ETA, v.TransitDays(), v.Via}
}

type bidJSON struct {
	ID          int64           `json:"id"`
	Amount      float64         `json:"amount"`
	VesselID    int64           `json:"vesselId"`
	Includes    map[string]bool `json:"includes"`
	Note        string          `json:"note"`
	SubmittedAt time.Time       `json:"submittedAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

func toBid(b *app.Bid) *bidJSON {
	if b == nil {
		return nil
	}
	inc := map[string]bool{}
	for k, v := range b.Includes {
		inc[string(k)] = v
	}
	return &bidJSON{b.ID, float64(b.AmountCents) / 100, b.VesselID, inc, b.Note, b.SubmittedAt, b.UpdatedAt}
}

type tenderJSON struct {
	ID         int64         `json:"id"`
	Ref        string        `json:"ref"`
	Lane       string        `json:"lane"`
	Origin     string        `json:"origin"`
	Dest       string        `json:"dest"`
	Week       int           `json:"week"`
	BoxCode    string        `json:"boxCode"`
	CBM        float64       `json:"cbm"`
	Tonnes     float64       `json:"tonnes"`
	Lots       int           `json:"lots"`
	Categories string        `json:"categories"`
	ClosesAt   time.Time     `json:"closesAt"`
	CFSCutoff  time.Time     `json:"cfsCutoff"`
	Status     string        `json:"status"`
	Open       bool          `json:"open"`
	Vessel     *vesselJSON   `json:"vessel"`
	Vessels    []*vesselJSON `json:"vessels,omitempty"`
	MyBid      *bidJSON      `json:"myBid"`
	Result     string        `json:"result"`
	Won        *float64      `json:"wonAmount,omitempty"`
	OriginCFS  string        `json:"originCfs"`
}

func (x *API) toTender(ft *app.ForwarderTender, full bool) tenderJSON {
	t := tenderJSON{ID: ft.ID, Ref: ft.Ref, Lane: ft.Sailing.Lane.Short(), Origin: ft.Sailing.Lane.Origin.Name, Dest: ft.Sailing.Lane.Dest.Name,
		Week: ft.Sailing.Week, BoxCode: ft.Container.BoxCode, CBM: ft.CBM, Tonnes: domain.Round(ft.Kg/1000, 1), Lots: ft.Lots, Categories: ft.CategoryLabel(),
		ClosesAt: ft.ClosesAt, CFSCutoff: ft.Sailing.CFSCutoff, Status: ft.Status, Open: ft.AcceptingBids(x.app.Now()), Vessel: toVessel(ft.Vessel),
		MyBid: toBid(ft.MyBid), Result: ft.Result, OriginCFS: ft.Sailing.Lane.OriginCFS}
	if ft.Result == "won" && ft.MyBid != nil {
		v := float64(ft.MyBid.AmountCents) / 100
		t.Won = &v
	}
	if full {
		for _, v := range ft.Vessels {
			t.Vessels = append(t.Vessels, toVessel(v))
		}
	}
	return t
}

type docJSON struct {
	ID         int64     `json:"id"`
	Kind       string    `json:"kind"`
	Label      string    `json:"label"`
	Filename   string    `json:"filename"`
	Size       string    `json:"size"`
	UploadedAt time.Time `json:"uploadedAt"`
	Deleted    bool      `json:"deleted"`
}

func toDoc(d *app.Document) docJSON {
	return docJSON{d.ID, d.Kind, app.DocKindLabel(d.Kind), d.Filename, d.SizeLabel(), d.UploadedAt, d.PurgedAt != nil}
}

// ---------- handlers ----------

func (x *API) meta(w http.ResponseWriter, r *http.Request) {
	var inc []map[string]string
	for _, i := range domain.Inclusions {
		inc = append(inc, map[string]string{"key": string(i), "label": i.Label()})
	}
	ok(w, map[string]any{"inclusions": inc, "devMode": x.app.Cfg.DevMode, "minAppVersion": "1.0.0"})
}

func (x *API) otp(w http.ResponseWriter, r *http.Request) {
	var in struct{ Phone string }
	if err := decode(r, &in); err != nil {
		x.err(w, err)
		return
	}
	if !x.allow("otp:"+ip(r), 10, 15*time.Minute) {
		fail(w, http.StatusTooManyRequests, "Too many codes requested. Wait a few minutes and try again.")
		return
	}
	phone, dev, err := x.app.RequestOTP(r.Context(), in.Phone)
	if err != nil {
		x.err(w, err)
		return
	}
	out := map[string]any{"phone": phone}
	if dev != "" {
		out["devCode"] = dev
	}
	ok(w, out)
}

func (x *API) verify(w http.ResponseWriter, r *http.Request) {
	var in struct{ Phone, Code string }
	if err := decode(r, &in); err != nil {
		x.err(w, err)
		return
	}
	if !x.allow("verify:"+ip(r), 20, 15*time.Minute) {
		fail(w, http.StatusTooManyRequests, "Too many tries. Wait a few minutes and try again.")
		return
	}
	u, created, err := x.app.VerifyOTP(r.Context(), in.Phone, in.Code)
	if err != nil {
		x.err(w, err)
		return
	}
	tok, err := x.app.CreateSession(r.Context(), u, "api", r.UserAgent())
	if err != nil {
		x.err(w, err)
		return
	}
	ok(w, map[string]any{"token": tok, "user": toUser(u), "newUser": created})
}

func (x *API) logout(w http.ResponseWriter, r *http.Request) {
	x.app.DeleteSession(r.Context(), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	ok(w, map[string]bool{"ok": true})
}

func (x *API) me(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	reg, err := x.app.Registration(r.Context(), u)
	if err != nil {
		x.err(w, err)
		return
	}
	out := map[string]any{"user": toUser(u), "registration": map[string]any{
		"hasCompany": reg.HasCompany, "hasBR": reg.HasBR, "hasLicence": reg.HasLicence, "hasAgent": reg.HasAgent,
		"lanes": reg.Lanes, "submitted": reg.Submitted, "status": reg.Status, "note": reg.StatusNote, "readyToSubmit": reg.ReadyToSubmit(),
	}}
	if u.CompanyID != 0 {
		c, err := x.app.CompanyByID(r.Context(), u.CompanyID)
		if err == nil {
			out["company"] = map[string]any{"id": c.ID, "name": c.Name, "regNumber": c.RegNumber, "email": c.Email, "status": c.Status, "laneIds": c.LaneIDs}
		}
	}
	ok(w, out)
}

func (x *API) profile(w http.ResponseWriter, r *http.Request) {
	var in struct{ CompanyName, RegNumber, ContactName, Email string }
	if err := decode(r, &in); err != nil {
		x.err(w, err)
		return
	}
	u, err := x.app.SaveForwarderProfile(r.Context(), userOf(r), app.ForwarderProfile{CompanyName: in.CompanyName, RegNumber: in.RegNumber, ContactName: in.ContactName, Email: in.Email})
	if err != nil {
		x.err(w, err)
		return
	}
	ok(w, map[string]any{"user": toUser(u)})
}

func (x *API) lanes(w http.ResponseWriter, r *http.Request) {
	ls, err := x.app.Lanes(r.Context(), true)
	if err != nil {
		x.err(w, err)
		return
	}
	out := []map[string]any{}
	for _, l := range ls {
		out = append(out, map[string]any{"id": l.ID, "label": l.Short(), "origin": l.Origin.Label(), "dest": l.Dest.Label()})
	}
	ok(w, out)
}

func (x *API) setLanes(w http.ResponseWriter, r *http.Request) {
	var in struct{ LaneIDs []int64 }
	if err := decode(r, &in); err != nil {
		x.err(w, err)
		return
	}
	if err := x.app.SetForwarderLanes(r.Context(), userOf(r), in.LaneIDs); err != nil {
		x.err(w, err)
		return
	}
	ok(w, map[string]bool{"ok": true})
}

func (x *API) myDocs(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	out := []docJSON{}
	if u.CompanyID != 0 {
		ds, err := x.app.DocsFor(r.Context(), "company", u.CompanyID)
		if err != nil {
			x.err(w, err)
			return
		}
		for _, d := range ds {
			out = append(out, toDoc(d))
		}
	}
	ok(w, out)
}

// readUpload reads a multipart "file" field.
func readUpload(r *http.Request) ([]byte, string, string, error) {
	if err := r.ParseMultipartForm(12 << 20); err != nil {
		return nil, "", "", app.Errf("That file is too large. The limit is 10 MB.")
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		return nil, "", "", app.Errf("Choose a file to upload.")
	}
	defer f.Close()
	data, ct, err := files.Sniff(f)
	if err != nil {
		if err == files.ErrTooLarge {
			return nil, "", "", app.Errf("That file is larger than 10 MB.")
		}
		return nil, "", "", app.Errf("Only PDF, JPG or PNG files are accepted.")
	}
	return data, ct, hdr.Filename, nil
}

func (x *API) uploadCompanyDoc(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	if u.CompanyID == 0 {
		fail(w, http.StatusBadRequest, "Add your company details first.")
		return
	}
	data, ct, name, err := readUpload(r)
	if err != nil {
		x.err(w, err)
		return
	}
	d, err := x.app.UploadDoc(r.Context(), u, "company", u.CompanyID, r.FormValue("kind"), name, data, ct)
	if err != nil {
		x.err(w, err)
		return
	}
	ok(w, toDoc(d))
}

func (x *API) submit(w http.ResponseWriter, r *http.Request) {
	if err := x.app.SubmitForwarder(r.Context(), userOf(r)); err != nil {
		x.err(w, err)
		return
	}
	ok(w, map[string]bool{"ok": true})
}

func (x *API) pushToken(w http.ResponseWriter, r *http.Request) {
	var in struct{ Token, Platform string }
	if err := decode(r, &in); err != nil {
		x.err(w, err)
		return
	}
	if err := x.app.SavePushToken(r.Context(), userOf(r).ID, in.Token, in.Platform); err != nil {
		x.err(w, err)
		return
	}
	ok(w, map[string]bool{"ok": true})
}

func (x *API) deletePushToken(w http.ResponseWriter, r *http.Request) {
	var in struct{ Token string }
	_ = decode(r, &in)
	_ = x.app.DeletePushToken(r.Context(), userOf(r).ID, in.Token)
	ok(w, map[string]bool{"ok": true})
}

func (x *API) notifications(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	ns, err := x.app.Notifications(r.Context(), u.ID, 50)
	if err != nil {
		x.err(w, err)
		return
	}
	out := []map[string]any{}
	for _, n := range ns {
		out = append(out, map[string]any{"id": n.ID, "title": n.Title, "body": n.Body, "read": n.Read, "createdAt": n.CreatedAt})
	}
	ok(w, map[string]any{"items": out, "unread": x.app.UnreadCount(r.Context(), u.ID)})
}

func (x *API) readNotifications(w http.ResponseWriter, r *http.Request) {
	var in struct{ ID int64 }
	_ = decode(r, &in)
	_ = x.app.MarkRead(r.Context(), userOf(r).ID, in.ID)
	ok(w, map[string]bool{"ok": true})
}

func (x *API) document(w http.ResponseWriter, r *http.Request) {
	d, data, err := x.app.OpenDoc(r.Context(), userOf(r), idParam(r))
	if err != nil {
		x.err(w, err)
		return
	}
	w.Header().Set("Content-Type", d.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, strings.ReplaceAll(d.Filename, `"`, "")))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Write(data)
}

func (x *API) tenders(w http.ResponseWriter, r *http.Request) {
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "open"
	}
	ts, err := x.app.ForwarderTenders(r.Context(), userOf(r), scope)
	if err != nil {
		x.err(w, err)
		return
	}
	out := []tenderJSON{}
	for _, t := range ts {
		out = append(out, x.toTender(t, false))
	}
	ok(w, out)
}

func (x *API) tender(w http.ResponseWriter, r *http.Request) {
	ft, err := x.app.ForwarderTenderByID(r.Context(), userOf(r), idParam(r))
	if err != nil {
		x.err(w, err)
		return
	}
	ok(w, x.toTender(ft, true))
}

func (x *API) bid(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Amount   float64
		VesselID int64
		Includes map[string]bool
		Note     string
	}
	if err := decode(r, &in); err != nil {
		x.err(w, err)
		return
	}
	inc := map[domain.Inclusion]bool{}
	for k, v := range in.Includes {
		inc[domain.Inclusion(k)] = v
	}
	if _, err := x.app.PlaceBid(r.Context(), userOf(r), app.BidInput{TenderID: idParam(r), AmountCents: domain.Cents(in.Amount), VesselID: in.VesselID, Includes: inc, Note: in.Note}); err != nil {
		x.err(w, err)
		return
	}
	x.tender(w, r)
}

func (x *API) withdraw(w http.ResponseWriter, r *http.Request) {
	if err := x.app.WithdrawBid(r.Context(), userOf(r), idParam(r)); err != nil {
		x.err(w, err)
		return
	}
	x.tender(w, r)
}

func (x *API) award(w http.ResponseWriter, r *http.Request) {
	d, err := x.app.AwardDetailFor(r.Context(), userOf(r), idParam(r))
	if err != nil {
		x.err(w, err)
		return
	}
	shippers := []map[string]any{}
	for _, s := range d.Shippers {
		docs := []docJSON{}
		for _, doc := range s.Docs {
			docs = append(docs, toDoc(doc))
		}
		shippers = append(shippers, map[string]any{
			"bookingRef": s.Booking.Ref, "company": s.Booking.CompanyName, "contactName": s.ContactName, "email": s.ContactEmail, "phone": s.ContactPhone,
			"cbm": s.Booking.CBM, "kg": s.Booking.Kg, "category": s.Booking.Category.Label(), "documents": docs,
		})
	}
	out := map[string]any{"tender": x.toTender(d.Tender, false), "shippers": shippers, "originCfs": d.OriginCFS}
	if d.Confirmation != nil {
		out["confirmation"] = toDoc(d.Confirmation)
	}
	ok(w, out)
}

func (x *API) confirmation(w http.ResponseWriter, r *http.Request) {
	data, ct, name, err := readUpload(r)
	if err != nil {
		x.err(w, err)
		return
	}
	d, err := x.app.UploadDoc(r.Context(), userOf(r), "tender", idParam(r), "booking_confirmation", name, data, ct)
	if err != nil {
		x.err(w, err)
		return
	}
	ok(w, toDoc(d))
}

func (x *API) documentLink(w http.ResponseWriter, r *http.Request) {
	q, err := x.app.SignDocLink(r.Context(), userOf(r), idParam(r), 10*time.Minute)
	if err != nil {
		x.err(w, err)
		return
	}
	ok(w, map[string]string{"url": x.app.Cfg.BaseURL + "/api/v1/files/" + chi.URLParam(r, "id") + "?" + q})
}

func (x *API) signedFile(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	uid, _ := strconv.ParseInt(q.Get("uid"), 10, 64)
	exp, _ := strconv.ParseInt(q.Get("exp"), 10, 64)
	d, data, err := x.app.OpenSignedDoc(r.Context(), idParam(r), uid, exp, q.Get("sig"))
	if err != nil {
		msg, _ := app.IsUserError(err)
		if msg == "" {
			msg = "Not found."
		}
		http.Error(w, msg, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", d.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, strings.ReplaceAll(d.Filename, `"`, "")))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Write(data)
}
