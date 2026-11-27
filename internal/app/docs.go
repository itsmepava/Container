package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/itsmepava/container/internal/domain"
	"github.com/jackc/pgx/v5"
)

type Document struct {
	ID          int64
	OwnerKind   string
	OwnerID     int64
	Kind        string
	Filename    string
	ContentType string
	Size        int64
	StorageKey  string
	UploadedBy  int64
	UploadedAt  time.Time
	PurgedAt    *time.Time
}

func (d *Document) SizeLabel() string {
	switch {
	case d.Size >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(d.Size)/(1<<20))
	case d.Size >= 1<<10:
		return fmt.Sprintf("%d KB", d.Size>>10)
	}
	return fmt.Sprintf("%d B", d.Size)
}

var docKinds = map[string]struct {
	Owner string
	Label string
	Help  string
}{
	"commercial_invoice":   {"booking", "Commercial invoice", "From your supplier, for customs."},
	"packing_list":         {"booking", "Supplier packing list", "Optional in phase 1."},
	"import_licence":       {"booking", "Import licence or permit", "Requested for this cargo category."},
	"booking_confirmation": {"tender", "Forwarder booking confirmation", "Uploaded by the forwarder."},
	"bl_copy":              {"tender", "Forwarder B/L copy", "Uploaded by the forwarder within 24 hours of award."},
	"br_certificate":       {"company", "Business registration", "Certificate of incorporation or BR."},
	"ff_licence":           {"company", "Freight forwarder licence", "Photo or PDF."},
	"origin_agent":         {"company", "Origin agent in China or India", "Optional: agency agreement or letter."},
}

func DocKindLabel(kind string) string {
	if k, ok := docKinds[kind]; ok {
		return k.Label
	}
	return kind
}

const docCols = `id,owner_kind,owner_id,kind,filename,content_type,size_bytes,storage_key,COALESCE(uploaded_by,0),uploaded_at,purged_at`

func scanDoc(row pgx.Row) (*Document, error) {
	d := &Document{}
	err := row.Scan(&d.ID, &d.OwnerKind, &d.OwnerID, &d.Kind, &d.Filename, &d.ContentType, &d.Size, &d.StorageKey, &d.UploadedBy, &d.UploadedAt, &d.PurgedAt)
	return d, err
}

func (a *App) DocsFor(ctx context.Context, ownerKind string, ownerID int64) ([]*Document, error) {
	rows, err := a.DB.Query(ctx, `SELECT `+docCols+` FROM documents WHERE owner_kind=$1 AND owner_id=$2 ORDER BY uploaded_at`, ownerKind, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Document
	for rows.Next() {
		d, err := scanDoc(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DocReq is one row of a document checklist.
type DocReq struct {
	Kind     string
	Label    string
	Help     string
	Required bool
	ByOther  bool // uploaded by someone else (the forwarder)
	Doc      *Document
}

func (r DocReq) Done() bool { return r.Doc != nil }

func licenceNeeded(st Settings, c domain.Category) bool {
	for _, x := range st.LicenceCategories {
		if x == c {
			return true
		}
	}
	return false
}

// BookingDocReqs is the checklist for a booking, including the forwarder's confirmation.
func (a *App) BookingDocReqs(ctx context.Context, b *Booking) ([]DocReq, error) {
	st := a.Settings(ctx)
	docs, err := a.DocsFor(ctx, "booking", b.ID)
	if err != nil {
		return nil, err
	}
	byKind := map[string]*Document{}
	for _, d := range docs {
		byKind[d.Kind] = d
	}
	reqs := []DocReq{
		{Kind: "commercial_invoice", Required: true},
		{Kind: "packing_list", Required: false},
	}
	if licenceNeeded(st, b.Category) || byKind["import_licence"] != nil {
		reqs = append(reqs, DocReq{Kind: "import_licence", Required: licenceNeeded(st, b.Category)})
	}
	for i := range reqs {
		k := docKinds[reqs[i].Kind]
		reqs[i].Label, reqs[i].Help, reqs[i].Doc = k.Label, k.Help, byKind[reqs[i].Kind]
	}
	if b.Mode == "pool" {
		conf := DocReq{Kind: "booking_confirmation", Label: docKinds["booking_confirmation"].Label, Help: docKinds["booking_confirmation"].Help, ByOther: true}
		bl := DocReq{Kind: "bl_copy", Label: docKinds["bl_copy"].Label, Help: docKinds["bl_copy"].Help, ByOther: true}
		if b.ContainerID != 0 {
			var tid int64
			if a.DB.QueryRow(ctx, `SELECT id FROM tenders WHERE container_id=$1 AND status='awarded'`, b.ContainerID).Scan(&tid) == nil {
				if tds, err := a.DocsFor(ctx, "tender", tid); err == nil {
					for _, d := range tds {
						switch d.Kind {
						case "booking_confirmation":
							conf.Doc = d
						case "bl_copy":
							bl.Doc = d
						}
					}
				}
			}
		}
		reqs = append(reqs, conf, bl)
	}
	return reqs, nil
}

// MissingDocs returns, per booking, the labels of required shipper documents not yet uploaded.
func (a *App) MissingDocs(ctx context.Context, bs []*Booking) (map[int64][]string, error) {
	st := a.Settings(ctx)
	out := map[int64][]string{}
	if len(bs) == 0 {
		return out, nil
	}
	var ids []int64
	for _, b := range bs {
		ids = append(ids, b.ID)
	}
	have := map[int64]map[string]bool{}
	rows, err := a.DB.Query(ctx, `SELECT owner_id, kind FROM documents WHERE owner_kind='booking' AND owner_id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var k string
		if err := rows.Scan(&id, &k); err != nil {
			rows.Close()
			return nil, err
		}
		if have[id] == nil {
			have[id] = map[string]bool{}
		}
		have[id][k] = true
	}
	rows.Close()
	for _, b := range bs {
		if !have[b.ID]["commercial_invoice"] {
			out[b.ID] = append(out[b.ID], "Invoice")
		}
		if licenceNeeded(st, b.Category) && !have[b.ID]["import_licence"] {
			out[b.ID] = append(out[b.ID], "Licence")
		}
	}
	return out, nil
}

// canWriteDoc checks whether u may upload a document of this kind to this owner.
func (a *App) canWriteDoc(ctx context.Context, u *User, ownerKind string, ownerID int64, kind string) error {
	k, ok := docKinds[kind]
	if !ok || k.Owner != ownerKind {
		return Errf("Unknown document type.")
	}
	switch ownerKind {
	case "booking":
		b, err := a.BookingFor(ctx, u, ownerID)
		if err != nil {
			return err
		}
		if b.Status != "booked" && b.Status != "held" {
			return Errf("This booking is %s, so documents can't be added.", strings.ToLower(b.StatusLabel()))
		}
		if !u.IsStaff() && !u.IsFactory() {
			return ErrForbidden
		}
	case "tender":
		if u.IsStaff() {
			return nil
		}
		if !u.IsForwarder() {
			return ErrForbidden
		}
		var winner int64
		err := a.DB.QueryRow(ctx, `SELECT COALESCE(b.company_id,0) FROM tenders t LEFT JOIN bids b ON b.id=t.awarded_bid_id WHERE t.id=$1 AND t.status='awarded'`, ownerID).Scan(&winner)
		if err != nil || winner != u.CompanyID {
			return Errf("Only the winning forwarder can upload the booking confirmation or B/L copy.")
		}
	case "company":
		if u.IsStaff() {
			return nil
		}
		if u.CompanyID != ownerID {
			return ErrForbidden
		}
	}
	return nil
}

// UploadDoc stores a document, replacing any earlier one of the same kind.
func (a *App) UploadDoc(ctx context.Context, u *User, ownerKind string, ownerID int64, kind, filename string, data []byte, contentType string) (*Document, error) {
	if err := a.canWriteDoc(ctx, u, ownerKind, ownerID, kind); err != nil {
		return nil, err
	}
	filename = filepath.Base(strings.TrimSpace(filename))
	if filename == "" || filename == "." {
		filename = kind
	}
	if len(filename) > 120 {
		filename = filename[len(filename)-120:]
	}
	key, err := a.Files.Put(data)
	if err != nil {
		return nil, err
	}
	var old []string
	var id int64
	err = a.Tx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `DELETE FROM documents WHERE owner_kind=$1 AND owner_id=$2 AND kind=$3 RETURNING storage_key`, ownerKind, ownerID, kind)
		if err != nil {
			return err
		}
		for rows.Next() {
			var k string
			_ = rows.Scan(&k)
			old = append(old, k)
		}
		rows.Close()
		if err := tx.QueryRow(ctx, `INSERT INTO documents(owner_kind,owner_id,kind,filename,content_type,size_bytes,storage_key,uploaded_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
			ownerKind, ownerID, kind, filename, contentType, len(data), key, u.ID).Scan(&id); err != nil {
			return err
		}
		if err := a.Audit(ctx, tx, u, "document.upload", ownerKind, ownerID, DocKindLabel(kind)+" uploaded: "+filename, nil); err != nil {
			return err
		}
		if kind == "booking_confirmation" {
			// Tell the shippers in the box.
			rows, err := tx.Query(ctx, `SELECT DISTINCT b.company_id, b.id FROM tenders t JOIN bookings b ON b.container_id=t.container_id WHERE t.id=$1 AND b.status='booked'`, ownerID)
			if err != nil {
				return err
			}
			type cb struct{ c, b int64 }
			var list []cb
			for rows.Next() {
				var x cb
				_ = rows.Scan(&x.c, &x.b)
				list = append(list, x)
			}
			rows.Close()
			for _, x := range list {
				if err := a.NotifyCompany(ctx, tx, x.c, Note{Title: "Forwarder booking confirmed", Body: "The forwarder uploaded its booking confirmation for your shipment.", Link: fmt.Sprintf("/f/bookings/%d", x.b)}); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE tenders SET handover_at=COALESCE(handover_at, now()) WHERE id=$1`, ownerID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		_ = a.Files.Delete(key)
		return nil, err
	}
	for _, k := range old {
		_ = a.Files.Delete(k)
	}
	return scanDoc(a.DB.QueryRow(ctx, `SELECT `+docCols+` FROM documents WHERE id=$1`, id))
}

// DeleteDoc removes a document the user uploaded (or any, for staff).
func (a *App) DeleteDoc(ctx context.Context, u *User, id int64) error {
	d, err := scanDoc(a.DB.QueryRow(ctx, `SELECT `+docCols+` FROM documents WHERE id=$1`, id))
	if err != nil {
		return ErrNotFound
	}
	if err := a.canWriteDoc(ctx, u, d.OwnerKind, d.OwnerID, d.Kind); err != nil {
		return err
	}
	err = a.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM documents WHERE id=$1`, id); err != nil {
			return err
		}
		return a.Audit(ctx, tx, u, "document.delete", d.OwnerKind, d.OwnerID, DocKindLabel(d.Kind)+" removed: "+d.Filename, nil)
	})
	if err == nil {
		_ = a.Files.Delete(d.StorageKey)
	}
	return err
}

// canReadDoc decides who may download a document.
func (a *App) canReadDoc(ctx context.Context, u *User, d *Document) bool {
	if u.IsStaff() {
		return true
	}
	var ok bool
	switch d.OwnerKind {
	case "booking":
		if u.IsFactory() {
			_ = a.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bookings WHERE id=$1 AND company_id=$2)`, d.OwnerID, u.CompanyID).Scan(&ok)
		} else if u.IsForwarder() {
			// The winning forwarder of the booking's container.
			_ = a.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bookings b JOIN tenders t ON t.container_id=b.container_id JOIN bids w ON w.id=t.awarded_bid_id
				WHERE b.id=$1 AND t.status='awarded' AND w.company_id=$2)`, d.OwnerID, u.CompanyID).Scan(&ok)
		}
	case "tender":
		if u.IsForwarder() {
			_ = a.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tenders t JOIN bids w ON w.id=t.awarded_bid_id WHERE t.id=$1 AND w.company_id=$2)`, d.OwnerID, u.CompanyID).Scan(&ok)
		} else if u.IsFactory() {
			_ = a.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tenders t JOIN bookings b ON b.container_id=t.container_id WHERE t.id=$1 AND b.company_id=$2)`, d.OwnerID, u.CompanyID).Scan(&ok)
		}
	case "company":
		ok = u.CompanyID == d.OwnerID
	}
	return ok
}

// OpenDoc returns a document's bytes for a user allowed to read it, and logs the view.
func (a *App) OpenDoc(ctx context.Context, u *User, id int64) (*Document, []byte, error) {
	d, err := scanDoc(a.DB.QueryRow(ctx, `SELECT `+docCols+` FROM documents WHERE id=$1`, id))
	if err != nil {
		return nil, nil, ErrNotFound
	}
	if !a.canReadDoc(ctx, u, d) {
		return nil, nil, ErrNotFound
	}
	if d.PurgedAt != nil {
		return nil, nil, Errf("This file was deleted after handover, as Eimsky keeps no copies.")
	}
	data, err := a.Files.Get(d.StorageKey)
	if err != nil {
		return nil, nil, err
	}
	_ = a.Audit(ctx, a.DB, u, "document.view", d.OwnerKind, d.OwnerID, DocKindLabel(d.Kind)+" viewed: "+d.Filename, nil)
	return d, data, nil
}

// PurgeDocuments deletes shipment documents a set number of days after the vessel sails.
func (a *App) PurgeDocuments(ctx context.Context) (int, error) {
	st := a.Settings(ctx)
	cutoff := a.Now().AddDate(0, 0, -st.DocRetentionDays)
	rows, err := a.DB.Query(ctx, `
		SELECT d.id, d.storage_key FROM documents d
		JOIN bookings b ON d.owner_kind='booking' AND b.id=d.owner_id
		JOIN sailings s ON s.id=b.sailing_id
		JOIN vessel_options v ON v.id=s.chosen_vessel_id
		WHERE d.purged_at IS NULL AND v.etd < $1 AND s.status IN ('awarded','coload','closed')
		UNION
		SELECT d.id, d.storage_key FROM documents d
		JOIN tenders t ON d.owner_kind='tender' AND t.id=d.owner_id
		JOIN vessel_options v ON v.id=t.vessel_id
		WHERE d.purged_at IS NULL AND v.etd < $1 AND t.status='awarded'`, cutoff)
	if err != nil {
		return 0, err
	}
	type item struct {
		id  int64
		key string
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.key); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, it)
	}
	rows.Close()
	for _, it := range items {
		if err := a.Files.Delete(it.key); err != nil {
			a.Log.Error("purge document", "id", it.id, "err", err)
			continue
		}
		_, _ = a.DB.Exec(ctx, `UPDATE documents SET purged_at=now() WHERE id=$1`, it.id)
	}
	if len(items) > 0 {
		_ = a.Audit(ctx, a.DB, nil, "document.purge", "documents", 0, fmt.Sprintf("Deleted %d shipment document(s) after handover", len(items)), nil)
	}
	return len(items), nil
}

// SignDocLink returns query parameters for a short-lived link to a document
// the user may read, so a phone can open it in the browser without its token.
func (a *App) SignDocLink(ctx context.Context, u *User, docID int64, ttl time.Duration) (string, error) {
	d, err := scanDoc(a.DB.QueryRow(ctx, `SELECT `+docCols+` FROM documents WHERE id=$1`, docID))
	if err != nil || !a.canReadDoc(ctx, u, d) {
		return "", ErrNotFound
	}
	exp := a.Now().Add(ttl).Unix()
	return fmt.Sprintf("uid=%d&exp=%d&sig=%s", u.ID, exp, a.docSig(docID, u.ID, exp)), nil
}

func (a *App) docSig(docID, uid, exp int64) string {
	m := hmac.New(sha256.New, a.Cfg.FileKey)
	fmt.Fprintf(m, "doc:%d:%d:%d", docID, uid, exp)
	return hex.EncodeToString(m.Sum(nil))
}

// OpenSignedDoc checks a signed link and returns the document.
func (a *App) OpenSignedDoc(ctx context.Context, docID, uid, exp int64, sig string) (*Document, []byte, error) {
	if a.Now().Unix() > exp || !hmac.Equal([]byte(sig), []byte(a.docSig(docID, uid, exp))) {
		return nil, nil, Errf("This link has expired. Open the document again from the app.")
	}
	u, err := a.UserByID(ctx, uid)
	if err != nil || !u.Active {
		return nil, nil, ErrNotFound
	}
	return a.OpenDoc(ctx, u, docID)
}
