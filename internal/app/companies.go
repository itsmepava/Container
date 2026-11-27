package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type Company struct {
	ID              int64
	Kind            string
	Name            string
	RegNumber       string
	Address         string
	Phone           string
	Email           string
	Status          string
	StatusNote      string
	SubmittedAt     *time.Time
	CreatedAt       time.Time
	Users           int
	LaneIDs         []int64
	InvoiceCurrency string
	FallbackLanes   int
}

func (c *Company) KindLabel() string {
	switch c.Kind {
	case "factory":
		return "Factory"
	case "forwarder":
		return "Forwarder"
	case "association":
		return "Association"
	}
	return c.Kind
}

const companyCols = `c.id,c.kind,c.name,c.reg_number,c.address,c.phone,c.email,c.status,c.status_note,c.submitted_at,c.created_at,(SELECT count(*) FROM users WHERE company_id=c.id AND active),c.invoice_currency`

func scanCompany(row pgx.Row) (*Company, error) {
	c := &Company{}
	err := row.Scan(&c.ID, &c.Kind, &c.Name, &c.RegNumber, &c.Address, &c.Phone, &c.Email, &c.Status, &c.StatusNote, &c.SubmittedAt, &c.CreatedAt, &c.Users, &c.InvoiceCurrency)
	return c, err
}

func (a *App) Companies(ctx context.Context, kind, status, q string) ([]*Company, error) {
	rows, err := a.DB.Query(ctx, `SELECT `+companyCols+` FROM companies c WHERE c.kind<>'eimsky' AND ($1='' OR c.kind=$1) AND ($2='' OR c.status=$2)
		AND ($3='' OR c.name ILIKE '%'||$3||'%') AND NOT (c.kind='forwarder' AND c.status='pending' AND c.submitted_at IS NULL)
		ORDER BY CASE c.status WHEN 'pending' THEN 0 ELSE 1 END, c.name`, kind, status, strings.TrimSpace(q))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Company
	for rows.Next() {
		c, err := scanCompany(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (a *App) CompanyByID(ctx context.Context, id int64) (*Company, error) {
	c, err := scanCompany(a.DB.QueryRow(ctx, `SELECT `+companyCols+` FROM companies c WHERE c.id=$1`, id))
	if err != nil {
		return nil, notFound(err)
	}
	c.LaneIDs, err = a.ForwarderLanes(ctx, id)
	return c, err
}

func (a *App) PendingApprovals(ctx context.Context) int {
	var n int
	_ = a.DB.QueryRow(ctx, `SELECT count(*) FROM companies WHERE status='pending' AND (kind<>'forwarder' OR submitted_at IS NOT NULL)`).Scan(&n)
	return n
}

// SetCompanyStatus approves, rejects or suspends a company and tells its users.
func (a *App) SetCompanyStatus(ctx context.Context, actor *User, id int64, status, note string) error {
	switch status {
	case "approved", "rejected", "suspended", "pending":
	default:
		return Errf("Unknown status.")
	}
	note = strings.TrimSpace(note)
	if (status == "rejected" || status == "suspended") && note == "" {
		return Errf("Add a short reason; the company will see it.")
	}
	c, err := a.CompanyByID(ctx, id)
	if err != nil {
		return err
	}
	return a.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE companies SET status=$2, status_note=$3 WHERE id=$1`, id, status, note); err != nil {
			return err
		}
		if err := a.Audit(ctx, tx, actor, "company."+status, "company", id, fmt.Sprintf("%s %s", c.Name, status), map[string]string{"note": note}); err != nil {
			return err
		}
		var n Note
		switch status {
		case "approved":
			n = Note{Title: "Your Eimsky account is approved", Body: c.Name + " can now use Eimsky.", Email: true, Push: true}
			if c.Kind == "factory" {
				n.Body = c.Name + " can now book space in pooled containers."
				n.Link = "/f/book"
			} else if c.Kind == "forwarder" {
				n.Body = c.Name + " will now get tenders on its lanes."
			}
		case "rejected":
			n = Note{Title: "Your Eimsky registration needs changes", Body: note, Email: true, Push: true}
		case "suspended":
			n = Note{Title: "Your Eimsky account is suspended", Body: note, Email: true, Push: true}
		default:
			return nil
		}
		return a.NotifyCompany(ctx, tx, id, n)
	})
}

// CreateCompany lets admins add a factory or association and invite its first user.
func (a *App) CreateCompany(ctx context.Context, actor *User, kind, name, regNumber, contactName, email string) (int64, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, "", Errf("Enter the company name.")
	}
	role := RoleFactoryAdmin
	switch kind {
	case "factory":
	case "association":
		role = RoleViewer
	default:
		return 0, "", Errf("Choose factory or association.")
	}
	var id int64
	if err := a.DB.QueryRow(ctx, `INSERT INTO companies(kind,name,reg_number,status,email) VALUES ($1,$2,$3,'approved',$4) RETURNING id`, kind, name, strings.TrimSpace(regNumber), strings.ToLower(strings.TrimSpace(email))).Scan(&id); err != nil {
		return 0, "", err
	}
	_ = a.Audit(ctx, a.DB, actor, "company.create", "company", id, "Created "+name, nil)
	_, link, err := a.InviteUser(ctx, actor, id, role, contactName, email)
	if err != nil {
		_, _ = a.DB.Exec(ctx, `DELETE FROM companies WHERE id=$1`, id)
		return 0, "", err
	}
	return id, link, nil
}

// UpdateCompanyProfile edits a company's details.
func (a *App) UpdateCompanyProfile(ctx context.Context, actor *User, id int64, name, reg, address, phone, currency string) error {
	if currency != "LKR" {
		currency = "USD"
	}
	if !actor.IsStaff() && (actor.CompanyID != id || (actor.Role != RoleFactoryAdmin && actor.Role != RoleForwarder)) {
		return ErrForbidden
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Errf("Enter the company name.")
	}
	if p := strings.TrimSpace(phone); p != "" {
		var err error
		if phone, err = NormalizePhone(p); err != nil {
			return err
		}
	}
	_, err := a.DB.Exec(ctx, `UPDATE companies SET name=$2, reg_number=$3, address=$4, phone=$5, invoice_currency=$6 WHERE id=$1`, id, name, strings.TrimSpace(reg), strings.TrimSpace(address), phone, currency)
	if err == nil {
		_ = a.Audit(ctx, a.DB, actor, "company.update", "company", id, "Updated company details", nil)
	}
	return err
}

func (a *App) UpdateProfile(ctx context.Context, u *User, name, phone string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return Errf("Enter your name.")
	}
	if p := strings.TrimSpace(phone); p != "" && !u.IsForwarder() {
		var err error
		if phone, err = NormalizePhone(p); err != nil {
			return err
		}
	} else if u.IsForwarder() {
		phone = u.Phone // a forwarder's phone is its login
	}
	_, err := a.DB.Exec(ctx, `UPDATE users SET name=$2, phone=$3 WHERE id=$1`, u.ID, name, phone)
	if err != nil && strings.Contains(err.Error(), "users_phone_uq") {
		return Errf("That phone number is used by another account.")
	}
	return err
}

// ---------- Forwarder registration (mobile) ----------

func (a *App) ForwarderLanes(ctx context.Context, companyID int64) ([]int64, error) {
	rows, err := a.DB.Query(ctx, `SELECT lane_id FROM forwarder_lanes WHERE company_id=$1 ORDER BY lane_id`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
		out = append(out, id)
	}
	return out, rows.Err()
}

type ForwarderProfile struct {
	CompanyName string
	RegNumber   string
	ContactName string
	Email       string
}

// SaveForwarderProfile creates or updates the forwarder's company (step 1 of registration).
func (a *App) SaveForwarderProfile(ctx context.Context, u *User, in ForwarderProfile) (*User, error) {
	if !u.IsForwarder() {
		return nil, ErrForbidden
	}
	in.CompanyName = strings.TrimSpace(in.CompanyName)
	in.ContactName = strings.TrimSpace(in.ContactName)
	if in.CompanyName == "" || in.ContactName == "" {
		return nil, Errf("Enter the company name and your name.")
	}
	email := ""
	if strings.TrimSpace(in.Email) != "" {
		var err error
		if email, err = NormalizeEmail(in.Email); err != nil {
			return nil, err
		}
	}
	err := a.Tx(ctx, func(tx pgx.Tx) error {
		if email != "" {
			var taken bool
			_ = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE lower(email)=$1 AND id<>$2)`, email, u.ID).Scan(&taken)
			if taken {
				return Errf("%s is used by another account.", email)
			}
		}
		if u.CompanyID == 0 {
			var cid int64
			if err := tx.QueryRow(ctx, `INSERT INTO companies(kind,name,reg_number,phone,email,status) VALUES ('forwarder',$1,$2,$3,$4,'pending') RETURNING id`,
				in.CompanyName, strings.TrimSpace(in.RegNumber), u.Phone, email).Scan(&cid); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE users SET company_id=$2 WHERE id=$1`, u.ID, cid); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec(ctx, `UPDATE companies SET name=$2, reg_number=$3, email=$4 WHERE id=$1`, u.CompanyID, in.CompanyName, strings.TrimSpace(in.RegNumber), email); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `UPDATE users SET name=$2, email=$3 WHERE id=$1`, u.ID, in.ContactName, email)
		return err
	})
	if err != nil {
		return nil, err
	}
	return a.UserByID(ctx, u.ID)
}

// SetForwarderLanes stores the lanes a forwarder serves.
func (a *App) SetForwarderLanes(ctx context.Context, u *User, laneIDs []int64) error {
	if !u.IsForwarder() || u.CompanyID == 0 {
		return Errf("Add your company details first.")
	}
	if len(laneIDs) == 0 {
		return Errf("Choose at least one lane.")
	}
	return a.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM forwarder_lanes WHERE company_id=$1`, u.CompanyID); err != nil {
			return err
		}
		for _, id := range laneIDs {
			if _, err := tx.Exec(ctx, `INSERT INTO forwarder_lanes(company_id,lane_id) SELECT $1,id FROM lanes WHERE id=$2 AND active ON CONFLICT DO NOTHING`, u.CompanyID, id); err != nil {
				return err
			}
		}
		// Approved forwarders also see open tenders on newly added lanes.
		if _, err := tx.Exec(ctx, `INSERT INTO tender_invites(tender_id,company_id)
			SELECT t.id, $1 FROM tenders t JOIN containers c ON c.id=t.container_id JOIN sailings s ON s.id=c.sailing_id
			JOIN companies co ON co.id=$1 AND co.status='approved'
			WHERE t.status='open' AND s.lane_id = ANY($2) ON CONFLICT DO NOTHING`, u.CompanyID, laneIDs); err != nil {
			return err
		}
		return a.Audit(ctx, tx, u, "forwarder.lanes", "company", u.CompanyID, fmt.Sprintf("Serves %d lane(s)", len(laneIDs)), laneIDs)
	})
}

// RegistrationState says what a forwarder still has to do.
type RegistrationState struct {
	HasCompany bool
	HasBR      bool
	HasLicence bool
	HasAgent   bool
	Lanes      int
	Submitted  bool
	Status     string
	StatusNote string
}

func (r RegistrationState) ReadyToSubmit() bool {
	return r.HasCompany && r.HasBR && r.HasLicence && r.Lanes > 0
}

func (a *App) Registration(ctx context.Context, u *User) (RegistrationState, error) {
	var r RegistrationState
	if u.CompanyID == 0 {
		return r, nil
	}
	c, err := a.CompanyByID(ctx, u.CompanyID)
	if err != nil {
		return r, err
	}
	r.HasCompany, r.Status, r.StatusNote, r.Submitted, r.Lanes = true, c.Status, c.StatusNote, c.SubmittedAt != nil, len(c.LaneIDs)
	docs, err := a.DocsFor(ctx, "company", u.CompanyID)
	if err != nil {
		return r, err
	}
	for _, d := range docs {
		switch d.Kind {
		case "br_certificate":
			r.HasBR = true
		case "ff_licence":
			r.HasLicence = true
		case "origin_agent":
			r.HasAgent = true
		}
	}
	return r, nil
}

// SubmitForwarder sends a completed registration to Eimsky for approval.
func (a *App) SubmitForwarder(ctx context.Context, u *User) error {
	r, err := a.Registration(ctx, u)
	if err != nil {
		return err
	}
	if !r.ReadyToSubmit() {
		return Errf("Add your company details, business registration, forwarder licence and at least one lane first.")
	}
	if r.Status == "approved" {
		return nil
	}
	return a.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE companies SET status='pending', status_note='', submitted_at=now() WHERE id=$1`, u.CompanyID); err != nil {
			return err
		}
		if err := a.Audit(ctx, tx, u, "forwarder.submit", "company", u.CompanyID, u.CompanyName+" submitted its registration", nil); err != nil {
			return err
		}
		return a.NotifyStaff(ctx, tx, Note{Title: "New forwarder to approve: " + u.CompanyName, Body: "Check the documents and approve within one working day.", Link: fmt.Sprintf("/admin/companies/%d", u.CompanyID), Email: true})
	})
}
