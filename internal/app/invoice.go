package app

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/go-pdf/fpdf"
	"github.com/itsmepava/container/internal/domain"
	"github.com/jackc/pgx/v5"
)

type Invoice struct {
	ID           int64
	Number       string
	BookingID    int64
	CompanyID    int64
	CompanyName  string
	BookingRef   string
	FreightCents int64
	FeeCents     int64
	TotalCents   int64
	Status       string
	IssuedAt     time.Time
	PaidAt       *time.Time
}

const invoiceCols = `i.id,i.number,i.booking_id,i.company_id,c.name,b.ref,i.freight_cents,i.fee_cents,i.total_cents,i.status,i.issued_at,i.paid_at`

func (a *App) queryInvoices(ctx context.Context, where string, args ...any) ([]*Invoice, error) {
	rows, err := a.DB.Query(ctx, `SELECT `+invoiceCols+` FROM invoices i JOIN companies c ON c.id=i.company_id JOIN bookings b ON b.id=i.booking_id WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Invoice
	for rows.Next() {
		v := &Invoice{}
		if err := rows.Scan(&v.ID, &v.Number, &v.BookingID, &v.CompanyID, &v.CompanyName, &v.BookingRef, &v.FreightCents, &v.FeeCents, &v.TotalCents, &v.Status, &v.IssuedAt, &v.PaidAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// InvoiceForBooking returns the booking's invoice, if issued, for a user allowed to see it.
func (a *App) InvoiceForBooking(ctx context.Context, u *User, bookingID int64) (*Invoice, error) {
	vs, err := a.queryInvoices(ctx, `i.booking_id=$1`, bookingID)
	if err != nil || len(vs) == 0 {
		return nil, ErrNotFound
	}
	if !u.IsStaff() && vs[0].CompanyID != u.CompanyID {
		return nil, ErrNotFound
	}
	return vs[0], nil
}

// Invoices lists invoices for a company (0 for all).
func (a *App) Invoices(ctx context.Context, companyID int64, status string) ([]*Invoice, error) {
	return a.queryInvoices(ctx, `($1=0 OR i.company_id=$1) AND ($2='' OR i.status=$2) ORDER BY i.issued_at DESC LIMIT 500`, companyID, status)
}

func (a *App) MarkInvoicePaid(ctx context.Context, actor *User, id int64, paid bool) error {
	status := "issued"
	if paid {
		status = "paid"
	}
	return a.Tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE invoices SET status=$2, paid_at=CASE WHEN $2='paid' THEN now() ELSE NULL END WHERE id=$1 AND status<>'void'`, id, status)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return a.Audit(ctx, tx, actor, "invoice."+status, "invoice", id, "Invoice marked "+status, nil)
	})
}

// IssueDueInvoices invoices priced bookings once their documents are complete.
func (a *App) IssueDueInvoices(ctx context.Context) (int, error) {
	bs, err := a.queryBookings(ctx, a.DB, `b.status='booked' AND b.priced_at IS NOT NULL AND NOT EXISTS (SELECT 1 FROM invoices WHERE booking_id=b.id)`)
	if err != nil {
		return 0, err
	}
	missing, err := a.MissingDocs(ctx, bs)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, b := range bs {
		if len(missing[b.ID]) > 0 {
			continue
		}
		if b.Mode == "pool" {
			var ok bool
			_ = a.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tenders t JOIN documents d ON d.owner_kind='tender' AND d.owner_id=t.id AND d.kind='booking_confirmation'
				WHERE t.container_id=$1 AND t.status='awarded')`, b.ContainerID).Scan(&ok)
			if !ok {
				continue
			}
		}
		err := a.Tx(ctx, func(tx pgx.Tx) error {
			var seq int64
			if err := tx.QueryRow(ctx, `SELECT nextval('invoice_seq')`).Scan(&seq); err != nil {
				return err
			}
			num := fmt.Sprintf("INV-%d-%06d", a.Now().In(domain.Colombo).Year(), seq)
			var id int64
			if err := tx.QueryRow(ctx, `INSERT INTO invoices(number,booking_id,company_id,freight_cents,fee_cents,total_cents) VALUES ($1,$2,$3,$4,$5,$6)
				ON CONFLICT (booking_id) DO NOTHING RETURNING id`, num, b.ID, b.CompanyID, *b.FreightCents, *b.FeeCents, *b.TotalCents).Scan(&id); err != nil {
				if err == pgx.ErrNoRows {
					return nil
				}
				return err
			}
			if err := a.Audit(ctx, tx, nil, "invoice.issue", "booking", b.ID, fmt.Sprintf("Invoice %s issued for %s: USD %s", num, b.Ref, domain.Money(*b.TotalCents)), nil); err != nil {
				return err
			}
			n++
			return a.NotifyCompany(ctx, tx, b.CompanyID, Note{Title: "Invoice " + num + " for " + b.Ref, Body: "USD " + domain.Money(*b.TotalCents) + ". Download it from My bookings.", Link: fmt.Sprintf("/f/bookings/%d", b.ID), Email: true})
		})
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// InvoicePDF renders an invoice as a PDF.
func (a *App) InvoicePDF(ctx context.Context, u *User, inv *Invoice) ([]byte, error) {
	b, err := a.BookingFor(ctx, u, inv.BookingID)
	if err != nil {
		return nil, err
	}
	var addr, reg string
	_ = a.DB.QueryRow(ctx, `SELECT address, reg_number FROM companies WHERE id=$1`, inv.CompanyID).Scan(&addr, &reg)
	vessel := b.Sailing.Vessel()
	carrier := ""
	if b.Mode == "pool" && b.ContainerID != 0 {
		_ = a.DB.QueryRow(ctx, `SELECT c.name FROM tenders t JOIN bids w ON w.id=t.awarded_bid_id JOIN companies c ON c.id=w.company_id WHERE t.container_id=$1`, b.ContainerID).Scan(&carrier)
	}

	p := fpdf.New("P", "mm", "A4", "")
	p.SetMargins(18, 18, 18)
	p.SetAutoPageBreak(true, 18)
	p.AddPage()
	tr := p.UnicodeTranslatorFromDescriptor("")
	navy := func() { p.SetTextColor(11, 78, 162) }
	ink := func() { p.SetTextColor(17, 24, 39) }
	muted := func() { p.SetTextColor(100, 116, 139) }

	p.SetFont("Courier", "B", 10)
	navy()
	p.CellFormat(0, 6, "EIMSKY", "", 1, "L", false, 0, "")
	p.SetFont("Helvetica", "B", 20)
	ink()
	p.CellFormat(0, 10, "Invoice "+inv.Number, "", 1, "L", false, 0, "")
	p.SetFont("Helvetica", "", 10)
	muted()
	p.CellFormat(0, 5, "Issued "+inv.IssuedAt.In(domain.Colombo).Format("2 January 2006"), "", 1, "L", false, 0, "")
	p.Ln(6)

	p.SetFont("Helvetica", "B", 10)
	ink()
	p.CellFormat(90, 5, "Bill to", "", 0, "L", false, 0, "")
	p.CellFormat(0, 5, "Shipment", "", 1, "L", false, 0, "")
	p.SetFont("Helvetica", "", 10)
	left := []string{inv.CompanyName}
	if reg != "" {
		left = append(left, "Reg. "+reg)
	}
	if addr != "" {
		left = append(left, addr)
	}
	right := []string{
		"Booking " + b.Ref,
		b.Sailing.Lane.Short() + " · " + b.Sailing.Label(),
		fmt.Sprintf("%.2f CBM · %s kg · %.2f W/M", b.CBM, domain.Thousands(int64(b.Kg)), b.WM),
	}
	if vessel != nil {
		right = append(right, vessel.Label()+" · ETD "+vessel.ETD.In(domain.Colombo).Format("2 Jan"))
	}
	if b.Mode == "coload" {
		right = append(right, "Co-load partner")
	} else if carrier != "" {
		right = append(right, "Carried by "+carrier)
	}
	for i := 0; i < len(left) || i < len(right); i++ {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		p.CellFormat(90, 5, tr(l), "", 0, "L", false, 0, "")
		p.CellFormat(0, 5, tr(r), "", 1, "L", false, 0, "")
	}
	p.Ln(8)

	p.SetFillColor(241, 245, 249)
	p.SetFont("Helvetica", "B", 10)
	p.CellFormat(124, 8, "Item", "B", 0, "L", true, 0, "")
	p.CellFormat(0, 8, "USD", "B", 1, "R", true, 0, "")
	p.SetFont("Helvetica", "", 10)
	freightLabel := fmt.Sprintf("Freight: your share, %.2f W/M of the winning bid", b.WM)
	if b.Mode == "coload" {
		freightLabel = fmt.Sprintf("Freight: co-load at partner rate, %.2f W/M", b.WM)
	}
	line := func(label string, cents int64) {
		p.CellFormat(124, 8, tr(label), "B", 0, "L", false, 0, "")
		p.CellFormat(0, 8, domain.Money(cents), "B", 1, "R", false, 0, "")
	}
	line(freightLabel, inv.FreightCents)
	line(fmt.Sprintf("Eimsky fee, %.2f W/M", b.WM), inv.FeeCents)
	if adj := inv.TotalCents - inv.FreightCents - inv.FeeCents; adj != 0 {
		line("Held at booking estimate", adj)
	}
	p.SetFont("Helvetica", "B", 12)
	p.CellFormat(124, 10, "Total due", "", 0, "L", false, 0, "")
	p.CellFormat(0, 10, "USD "+domain.Money(inv.TotalCents), "", 1, "R", false, 0, "")
	p.Ln(6)
	p.SetFont("Helvetica", "", 9)
	muted()
	p.MultiCell(0, 4.5, tr("All-in price per container covers ocean freight, origin charges and CFS stuffing, destination THC and delivery order, and de-stuffing at the Colombo CFS. Your final cost never goes above the estimate shown at booking (USD "+domain.Money(b.CapCents)+")."), "", "L", false)
	if inv.Status == "paid" && inv.PaidAt != nil {
		p.Ln(4)
		p.SetFont("Helvetica", "B", 11)
		p.SetTextColor(19, 122, 82)
		p.CellFormat(0, 6, "PAID "+inv.PaidAt.In(domain.Colombo).Format("2 Jan 2006"), "", 1, "L", false, 0, "")
	}
	var buf bytes.Buffer
	if err := p.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
