// Package app is Eimsky's service layer: every workflow (booking, planning,
// tendering, billing, documents, notifications) lives here and is shared by
// the web app, the mobile API and the background jobs.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/itsmepava/container/internal/config"
	"github.com/itsmepava/container/internal/domain"
	"github.com/itsmepava/container/internal/files"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Querier is satisfied by both the pool and a transaction.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type App struct {
	DB    *pgxpool.Pool
	Files *files.Store
	Cfg   config.Config
	Log   *slog.Logger
	Now   func() time.Time
	// SendSMS delivers an OTP text; replaced in tests.
	SendSMS func(ctx context.Context, to, msg string) error
}

func New(pool *pgxpool.Pool, fs *files.Store, cfg config.Config, log *slog.Logger) *App {
	a := &App{DB: pool, Files: fs, Cfg: cfg, Log: log, Now: time.Now}
	a.SendSMS = a.defaultSMS
	return a
}

// UserError carries a message that is safe and useful to show to the user.
type UserError struct{ Msg string }

func (e *UserError) Error() string { return e.Msg }

// Errf builds a UserError.
func Errf(format string, args ...any) error { return &UserError{Msg: fmt.Sprintf(format, args...)} }

var (
	ErrNotFound  = &UserError{Msg: "We couldn't find that. It may have been removed."}
	ErrForbidden = &UserError{Msg: "You don't have access to that."}
)

// IsUserError reports whether err's message can be shown as-is.
func IsUserError(err error) (string, bool) {
	var ue *UserError
	if errors.As(err, &ue) {
		return ue.Msg, true
	}
	return "", false
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// Tx runs fn in a transaction.
func (a *App) Tx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, a.DB, fn)
}

// ---------- Users ----------

type Role string

const (
	RoleAdmin        Role = "admin"
	RoleOps          Role = "ops"
	RoleFactoryAdmin Role = "factory_admin"
	RoleFactoryStaff Role = "factory_staff"
	RoleForwarder    Role = "forwarder"
	RoleViewer       Role = "viewer"
)

func (r Role) Label() string {
	switch r {
	case RoleAdmin:
		return "Eimsky admin"
	case RoleOps:
		return "Eimsky ops"
	case RoleFactoryAdmin:
		return "Factory admin"
	case RoleFactoryStaff:
		return "Factory staff"
	case RoleForwarder:
		return "Forwarder"
	case RoleViewer:
		return "Association viewer"
	}
	return string(r)
}

type User struct {
	ID            int64
	CompanyID     int64
	Role          Role
	Name          string
	Email         string
	Phone         string
	Active        bool
	EmailNotify   bool
	CompanyName   string
	CompanyKind   string
	CompanyStatus string
	LastLoginAt   *time.Time
	CreatedAt     time.Time
}

func (u *User) IsStaff() bool { return u != nil && (u.Role == RoleAdmin || u.Role == RoleOps) }
func (u *User) IsAdmin() bool { return u != nil && u.Role == RoleAdmin }
func (u *User) IsFactory() bool {
	return u != nil && (u.Role == RoleFactoryAdmin || u.Role == RoleFactoryStaff)
}
func (u *User) IsForwarder() bool { return u != nil && u.Role == RoleForwarder }
func (u *User) IsViewer() bool    { return u != nil && u.Role == RoleViewer }
func (u *User) Approved() bool {
	return u != nil && (u.CompanyID == 0 || u.CompanyStatus == "approved")
}

// Initials for avatars.
func (u *User) Initials() string {
	parts := strings.Fields(u.Name)
	s := ""
	for _, p := range parts {
		s += strings.ToUpper(p[:1])
		if len(s) == 2 {
			break
		}
	}
	if s == "" {
		return "?"
	}
	return s
}

const userCols = `u.id, COALESCE(u.company_id,0), u.role, u.name, u.email, u.phone, u.active, u.email_notify,
	COALESCE(c.name,'Eimsky'), COALESCE(c.kind,'eimsky'), COALESCE(c.status,'approved'), u.last_login_at, u.created_at`

func scanUser(row pgx.Row) (*User, error) {
	u := &User{}
	var role string
	err := row.Scan(&u.ID, &u.CompanyID, &role, &u.Name, &u.Email, &u.Phone, &u.Active, &u.EmailNotify,
		&u.CompanyName, &u.CompanyKind, &u.CompanyStatus, &u.LastLoginAt, &u.CreatedAt)
	u.Role = Role(role)
	return u, err
}

func (a *App) UserByID(ctx context.Context, id int64) (*User, error) {
	u, err := scanUser(a.DB.QueryRow(ctx, `SELECT `+userCols+` FROM users u LEFT JOIN companies c ON c.id=u.company_id WHERE u.id=$1`, id))
	return u, notFound(err)
}

func (a *App) UsersWhere(ctx context.Context, where string, args ...any) ([]*User, error) {
	rows, err := a.DB.Query(ctx, `SELECT `+userCols+` FROM users u LEFT JOIN companies c ON c.id=u.company_id WHERE `+where+` ORDER BY u.name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ---------- Settings ----------

type Settings struct {
	FeePerWM          float64
	Stowage           float64
	HoldMinutes       int
	KAnon             int
	DocRetentionDays  int
	LicenceCategories []domain.Category
	MinBidHours       int
	ReminderHours     int
}

var settingDefaults = map[string]string{
	"fee_per_wm":         "8",
	"stowage":            "0.85",
	"hold_minutes":       "15",
	"k_anon":             "3",
	"doc_retention_days": "7",
	"licence_categories": "fabric,dyes",
	"min_bid_hours":      "12",
	"reminder_hours":     "24",
}

// SettingKeys lists the editable settings with their labels and help.
var SettingKeys = []struct{ Key, Label, Help string }{
	{"fee_per_wm", "Eimsky fee (USD per W/M)", "Added to each shipper's share of the winning bid."},
	{"stowage", "Stowage factor", "Share of a box's internal volume that can be sold, e.g. 0.85."},
	{"hold_minutes", "Booking hold (minutes)", "How long space is held while a shipper confirms."},
	{"min_bid_hours", "Minimum bidding window (hours)", "If a sailing's bid close is sooner, tenders stay open this long."},
	{"reminder_hours", "Document reminder (hours before cut-off)", "When to remind shippers about missing documents."},
	{"licence_categories", "Categories needing an import licence", "Comma-separated: fabric, trims, packaging, dyes."},
	{"doc_retention_days", "Delete documents after ETD (days)", "Shipper documents are deleted this many days after the vessel sails."},
	{"k_anon", "Minimum factories per insight figure", "Dashboard figures from fewer factories are hidden."},
}

func (a *App) SettingsMap(ctx context.Context) (map[string]string, error) {
	m := map[string]string{}
	for k, v := range settingDefaults {
		m[k] = v
	}
	rows, err := a.DB.Query(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		m[k] = v
	}
	return m, rows.Err()
}

func (a *App) Settings(ctx context.Context) Settings {
	m, err := a.SettingsMap(ctx)
	if err != nil {
		m = settingDefaults
	}
	f := func(k string) float64 {
		v, err := strconv.ParseFloat(m[k], 64)
		if err != nil {
			v, _ = strconv.ParseFloat(settingDefaults[k], 64)
		}
		return v
	}
	s := Settings{
		FeePerWM: f("fee_per_wm"), Stowage: f("stowage"), HoldMinutes: int(f("hold_minutes")), KAnon: int(f("k_anon")),
		DocRetentionDays: int(f("doc_retention_days")), MinBidHours: int(f("min_bid_hours")), ReminderHours: int(f("reminder_hours")),
	}
	for _, c := range strings.Split(m["licence_categories"], ",") {
		c := domain.Category(strings.TrimSpace(c))
		if c.Valid() {
			s.LicenceCategories = append(s.LicenceCategories, c)
		}
	}
	return s
}

// SaveSettings validates and stores settings.
func (a *App) SaveSettings(ctx context.Context, actor *User, vals map[string]string) error {
	for k, v := range vals {
		if _, ok := settingDefaults[k]; !ok {
			return Errf("unknown setting %s", k)
		}
		v = strings.TrimSpace(v)
		if k != "licence_categories" {
			n, err := strconv.ParseFloat(v, 64)
			if err != nil || n < 0 {
				return Errf("%s must be a positive number", k)
			}
			if k == "stowage" && (n <= 0.3 || n > 1) {
				return Errf("stowage factor must be between 0.3 and 1")
			}
		}
		vals[k] = v
	}
	return a.Tx(ctx, func(tx pgx.Tx) error {
		for k, v := range vals {
			if _, err := tx.Exec(ctx, `INSERT INTO settings(key,value) VALUES($1,$2) ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value`, k, v); err != nil {
				return err
			}
		}
		return a.Audit(ctx, tx, actor, "settings.update", "settings", 0, "Settings changed", vals)
	})
}

// ---------- Audit ----------

// Audit appends an entry to the audit log.
func (a *App) Audit(ctx context.Context, q Querier, actor *User, action, entity string, entityID int64, summary string, detail any) error {
	var actorID *int64
	name := "system"
	if actor != nil {
		actorID = &actor.ID
		name = actor.Name
	}
	d := []byte("{}")
	if detail != nil {
		if b, err := json.Marshal(detail); err == nil {
			d = b
		}
	}
	var eid *int64
	if entityID != 0 {
		eid = &entityID
	}
	_, err := q.Exec(ctx, `INSERT INTO audit_log(actor_id, actor_name, action, entity, entity_id, summary, detail) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		actorID, name, action, entity, eid, summary, d)
	return err
}

type AuditEntry struct {
	ID        int64
	At        time.Time
	ActorName string
	Action    string
	Entity    string
	EntityID  int64
	Summary   string
	Detail    string
}

func (a *App) AuditList(ctx context.Context, entity string, entityID int64, q string, limit int) ([]AuditEntry, error) {
	sql := `SELECT id, at, actor_name, action, entity, COALESCE(entity_id,0), summary, detail::text FROM audit_log WHERE true`
	var args []any
	if entity != "" {
		args = append(args, entity)
		sql += fmt.Sprintf(" AND entity=$%d", len(args))
	}
	if entityID != 0 {
		args = append(args, entityID)
		sql += fmt.Sprintf(" AND entity_id=$%d", len(args))
	}
	if q != "" {
		args = append(args, "%"+q+"%")
		sql += fmt.Sprintf(" AND (summary ILIKE $%d OR actor_name ILIKE $%d OR action ILIKE $%d)", len(args), len(args), len(args))
	}
	args = append(args, limit)
	sql += fmt.Sprintf(" ORDER BY at DESC, id DESC LIMIT $%d", len(args))
	rows, err := a.DB.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.At, &e.ActorName, &e.Action, &e.Entity, &e.EntityID, &e.Summary, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---------- Notifications ----------

// Note is a message to one or more users, delivered in-app and optionally by email and push.
type Note struct {
	Title string
	Body  string
	Link  string // web path, e.g. /f/bookings/12
	Email bool
	Push  bool
	Data  map[string]any // extra push payload for the mobile app
}

// Notify queues a note for users inside the caller's transaction.
func (a *App) Notify(ctx context.Context, q Querier, userIDs []int64, n Note) error {
	seen := map[int64]bool{}
	for _, id := range userIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		if _, err := q.Exec(ctx, `INSERT INTO notifications(user_id,title,body,link) VALUES ($1,$2,$3,$4)`, id, n.Title, n.Body, n.Link); err != nil {
			return err
		}
		if n.Email {
			body := n.Body
			if n.Link != "" {
				body += "\n\nOpen in Eimsky: " + a.Cfg.BaseURL + n.Link
			}
			if _, err := q.Exec(ctx, `INSERT INTO email_outbox(to_addr,subject,body)
				SELECT email, $2, $3 FROM users WHERE id=$1 AND email<>'' AND email_notify AND active`, id, n.Title, body); err != nil {
				return err
			}
		}
		if n.Push {
			data, _ := json.Marshal(n.Data)
			if n.Data == nil {
				data = []byte("{}")
			}
			if _, err := q.Exec(ctx, `INSERT INTO push_outbox(user_id,title,body,data)
				SELECT $1,$2,$3,$4 WHERE EXISTS (SELECT 1 FROM push_tokens WHERE user_id=$1)`, id, n.Title, n.Body, data); err != nil {
				return err
			}
		}
	}
	return nil
}

// usersOf returns active user ids matching a condition.
func usersOf(ctx context.Context, q Querier, where string, args ...any) ([]int64, error) {
	rows, err := q.Query(ctx, `SELECT id FROM users WHERE active AND `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// NotifyStaff notifies every ops and admin user.
func (a *App) NotifyStaff(ctx context.Context, q Querier, n Note) error {
	ids, err := usersOf(ctx, q, `role IN ('admin','ops')`)
	if err != nil {
		return err
	}
	return a.Notify(ctx, q, ids, n)
}

// NotifyCompany notifies every active user of a company.
func (a *App) NotifyCompany(ctx context.Context, q Querier, companyID int64, n Note) error {
	ids, err := usersOf(ctx, q, `company_id=$1`, companyID)
	if err != nil {
		return err
	}
	return a.Notify(ctx, q, ids, n)
}

type Notification struct {
	ID        int64
	Title     string
	Body      string
	Link      string
	Read      bool
	CreatedAt time.Time
}

func (a *App) Notifications(ctx context.Context, userID int64, limit int) ([]Notification, error) {
	rows, err := a.DB.Query(ctx, `SELECT id,title,body,link,read_at IS NOT NULL,created_at FROM notifications WHERE user_id=$1 ORDER BY created_at DESC, id DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notification
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.Title, &n.Body, &n.Link, &n.Read, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (a *App) UnreadCount(ctx context.Context, userID int64) int {
	var n int
	_ = a.DB.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id=$1 AND read_at IS NULL`, userID).Scan(&n)
	return n
}

// MarkRead marks one notification (or all when id is 0) as read.
func (a *App) MarkRead(ctx context.Context, userID, id int64) error {
	if id == 0 {
		_, err := a.DB.Exec(ctx, `UPDATE notifications SET read_at=now() WHERE user_id=$1 AND read_at IS NULL`, userID)
		return err
	}
	_, err := a.DB.Exec(ctx, `UPDATE notifications SET read_at=now() WHERE user_id=$1 AND id=$2 AND read_at IS NULL`, userID, id)
	return err
}

func (a *App) SetEmailNotify(ctx context.Context, userID int64, on bool) error {
	_, err := a.DB.Exec(ctx, `UPDATE users SET email_notify=$2 WHERE id=$1`, userID, on)
	return err
}

func (a *App) SavePushToken(ctx context.Context, userID int64, token, platform string) error {
	token = strings.TrimSpace(token)
	if token == "" || len(token) > 300 {
		return Errf("invalid push token")
	}
	_, err := a.DB.Exec(ctx, `INSERT INTO push_tokens(token,user_id,platform) VALUES ($1,$2,$3)
		ON CONFLICT (token) DO UPDATE SET user_id=EXCLUDED.user_id, platform=EXCLUDED.platform`, token, userID, platform)
	return err
}

func (a *App) DeletePushToken(ctx context.Context, userID int64, token string) error {
	_, err := a.DB.Exec(ctx, `DELETE FROM push_tokens WHERE token=$1 AND user_id=$2`, token, userID)
	return err
}

// Mark records a one-off job key; returns false if it was already marked.
func (a *App) Mark(ctx context.Context, q Querier, key string) (bool, error) {
	tag, err := q.Exec(ctx, `INSERT INTO job_marks(key) VALUES ($1) ON CONFLICT DO NOTHING`, key)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}
