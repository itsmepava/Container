package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	WebSessionTTL = 14 * 24 * time.Hour
	APISessionTTL = 90 * 24 * time.Hour
)

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashToken(t string) string {
	s := sha256.Sum256([]byte(t))
	return hex.EncodeToString(s[:])
}

// BcryptCost is lowered in tests.
var BcryptCost = 12

func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), BcryptCost)
	return string(b), err
}

func ValidatePassword(pw string) error {
	if len(pw) < 8 {
		return Errf("Use at least 8 characters for the password.")
	}
	if len(pw) > 72 {
		return Errf("Use at most 72 characters for the password.")
	}
	return nil
}

func NormalizeEmail(e string) (string, error) {
	e = strings.ToLower(strings.TrimSpace(e))
	if e == "" {
		return "", Errf("Enter an email address.")
	}
	addr, err := mail.ParseAddress(e)
	if err != nil || addr.Address != e || !strings.Contains(e[strings.Index(e, "@"):], ".") {
		return "", Errf("%q isn't a valid email address.", e)
	}
	return e, nil
}

var phoneDigits = regexp.MustCompile(`\D`)

// NormalizePhone turns local Sri Lankan numbers into +94 format and keeps
// other international numbers as + and digits.
func NormalizePhone(p string) (string, error) {
	p = strings.TrimSpace(p)
	plus := strings.HasPrefix(p, "+")
	d := phoneDigits.ReplaceAllString(p, "")
	switch {
	case plus:
	case strings.HasPrefix(d, "00"):
		d = d[2:]
	case strings.HasPrefix(d, "0") && len(d) == 10:
		d = "94" + d[1:]
	case len(d) == 9:
		d = "94" + d
	}
	if len(d) < 9 || len(d) > 15 {
		return "", Errf("Enter a mobile number like +94 77 123 4567.")
	}
	return "+" + d, nil
}

// dummyHash makes failed logins for unknown emails take as long as real ones.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("not-a-real-password"), 10)

// Login checks an email and password.
func (a *App) Login(ctx context.Context, email, pw string) (*User, error) {
	bad := Errf("That email and password don't match. Check them and try again.")
	email = strings.ToLower(strings.TrimSpace(email))
	var id int64
	var hash string
	err := a.DB.QueryRow(ctx, `SELECT id, password_hash FROM users WHERE lower(email)=$1`, email).Scan(&id, &hash)
	if errors.Is(err, pgx.ErrNoRows) || hash == "" {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(pw))
		return nil, bad
	}
	if err != nil {
		return nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) != nil {
		return nil, bad
	}
	u, err := a.UserByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !u.Active {
		return nil, Errf("This account has been deactivated. Ask your administrator to turn it back on.")
	}
	return u, nil
}

// Session is a logged-in session.
type Session struct {
	User *User
	CSRF string
	Kind string
}

// CreateSession starts a session and returns the raw token for the cookie or bearer header.
func (a *App) CreateSession(ctx context.Context, u *User, kind, ua string) (string, error) {
	tok := randomToken()
	ttl := WebSessionTTL
	if kind == "api" {
		ttl = APISessionTTL
	}
	if len(ua) > 300 {
		ua = ua[:300]
	}
	_, err := a.DB.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,kind,csrf,user_agent,expires_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		hashToken(tok), u.ID, kind, randomToken(), ua, a.Now().Add(ttl))
	if err != nil {
		return "", err
	}
	_, _ = a.DB.Exec(ctx, `UPDATE users SET last_login_at=now() WHERE id=$1`, u.ID)
	return tok, nil
}

// SessionByToken loads the session and user for a raw token.
func (a *App) SessionByToken(ctx context.Context, tok, kind string) (*Session, error) {
	if tok == "" {
		return nil, ErrNotFound
	}
	var uid int64
	var csrf string
	var exp time.Time
	err := a.DB.QueryRow(ctx, `SELECT user_id, csrf, expires_at FROM sessions WHERE token_hash=$1 AND kind=$2`, hashToken(tok), kind).Scan(&uid, &csrf, &exp)
	if err != nil {
		return nil, notFound(err)
	}
	if a.Now().After(exp) {
		_, _ = a.DB.Exec(ctx, `DELETE FROM sessions WHERE token_hash=$1`, hashToken(tok))
		return nil, ErrNotFound
	}
	u, err := a.UserByID(ctx, uid)
	if err != nil {
		return nil, err
	}
	if !u.Active {
		return nil, ErrNotFound
	}
	return &Session{User: u, CSRF: csrf, Kind: kind}, nil
}

func (a *App) DeleteSession(ctx context.Context, tok string) {
	_, _ = a.DB.Exec(ctx, `DELETE FROM sessions WHERE token_hash=$1`, hashToken(tok))
}

// CheckCSRF compares tokens in constant time.
func CheckCSRF(want, got string) bool {
	return want != "" && subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1
}

// ---------- One-time links (reset, invite) ----------

func (a *App) createLink(ctx context.Context, q Querier, kind string, userID int64, ttl time.Duration) (string, error) {
	tok := randomToken()
	_, err := q.Exec(ctx, `INSERT INTO one_time_tokens(kind,user_id,token_hash,expires_at) VALUES ($1,$2,$3,$4)`, kind, userID, hashToken(tok), a.Now().Add(ttl))
	return tok, err
}

// LinkUser returns the user a valid link belongs to, without using it.
func (a *App) LinkUser(ctx context.Context, kind, tok string) (*User, error) {
	var uid int64
	var exp time.Time
	var used *time.Time
	err := a.DB.QueryRow(ctx, `SELECT user_id, expires_at, used_at FROM one_time_tokens WHERE token_hash=$1 AND kind=$2`, hashToken(tok), kind).Scan(&uid, &exp, &used)
	if err != nil || used != nil || a.Now().After(exp) {
		return nil, Errf("This link has expired or was already used. Ask for a new one.")
	}
	return a.UserByID(ctx, uid)
}

// RequestPasswordReset emails a reset link if the account exists. It never
// reveals whether the email is registered.
func (a *App) RequestPasswordReset(ctx context.Context, email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	var uid int64
	if err := a.DB.QueryRow(ctx, `SELECT id FROM users WHERE lower(email)=$1 AND active`, email).Scan(&uid); err != nil {
		return nil
	}
	return a.Tx(ctx, func(tx pgx.Tx) error {
		tok, err := a.createLink(ctx, tx, "reset", uid, 2*time.Hour)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO email_outbox(to_addr,subject,body) VALUES ($1,$2,$3)`, email, "Reset your Eimsky password",
			"Someone asked to reset the password for your Eimsky account.\n\nChoose a new password here (the link works for 2 hours):\n"+
				a.Cfg.BaseURL+"/reset?token="+tok+"\n\nIf it wasn't you, ignore this email.")
		return err
	})
}

// SetPasswordWithLink uses a reset or invite link to set a password.
func (a *App) SetPasswordWithLink(ctx context.Context, kind, tok, pw string) (*User, error) {
	if err := ValidatePassword(pw); err != nil {
		return nil, err
	}
	u, err := a.LinkUser(ctx, kind, tok)
	if err != nil {
		return nil, err
	}
	hash, err := HashPassword(pw)
	if err != nil {
		return nil, err
	}
	err = a.Tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE one_time_tokens SET used_at=now() WHERE token_hash=$1 AND used_at IS NULL`, hashToken(tok))
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return Errf("This link was already used.")
		}
		if _, err := tx.Exec(ctx, `UPDATE users SET password_hash=$2 WHERE id=$1`, u.ID, hash); err != nil {
			return err
		}
		// Sign out other sessions after a password change.
		if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1 AND kind='web'`, u.ID); err != nil {
			return err
		}
		return a.Audit(ctx, tx, u, "user.password_set", "user", u.ID, "Password set via "+kind+" link", nil)
	})
	return u, err
}

// ChangePassword changes a logged-in user's password.
func (a *App) ChangePassword(ctx context.Context, u *User, current, next string) error {
	if _, err := a.Login(ctx, u.Email, current); err != nil {
		return Errf("Your current password isn't right.")
	}
	if err := ValidatePassword(next); err != nil {
		return err
	}
	hash, err := HashPassword(next)
	if err != nil {
		return err
	}
	_, err = a.DB.Exec(ctx, `UPDATE users SET password_hash=$2 WHERE id=$1`, u.ID, hash)
	return err
}

// ---------- Factory sign-up and team ----------

type SignupInput struct {
	CompanyName string
	RegNumber   string
	Address     string
	Name        string
	Email       string
	Phone       string
	Password    string
}

// SignupFactory registers a factory and its first admin. The company waits for Eimsky approval.
func (a *App) SignupFactory(ctx context.Context, in SignupInput) (*User, error) {
	in.CompanyName = strings.TrimSpace(in.CompanyName)
	in.Name = strings.TrimSpace(in.Name)
	if in.CompanyName == "" || in.Name == "" {
		return nil, Errf("Enter your factory's name and your own name.")
	}
	email, err := NormalizeEmail(in.Email)
	if err != nil {
		return nil, err
	}
	phone := ""
	if strings.TrimSpace(in.Phone) != "" {
		if phone, err = NormalizePhone(in.Phone); err != nil {
			return nil, err
		}
	}
	if err := ValidatePassword(in.Password); err != nil {
		return nil, err
	}
	hash, err := HashPassword(in.Password)
	if err != nil {
		return nil, err
	}
	var uid int64
	err = a.Tx(ctx, func(tx pgx.Tx) error {
		var exists bool
		_ = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE lower(email)=$1)`, email).Scan(&exists)
		if exists {
			return Errf("An account with %s already exists. Sign in or reset your password.", email)
		}
		var cid int64
		if err := tx.QueryRow(ctx, `INSERT INTO companies(kind,name,reg_number,address,phone,email,status,submitted_at) VALUES ('factory',$1,$2,$3,$4,$5,'pending',now()) RETURNING id`,
			in.CompanyName, strings.TrimSpace(in.RegNumber), strings.TrimSpace(in.Address), phone, email).Scan(&cid); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO users(company_id,role,name,email,phone,password_hash) VALUES ($1,'factory_admin',$2,$3,$4,$5) RETURNING id`,
			cid, in.Name, email, phone, hash).Scan(&uid); err != nil {
			return err
		}
		if err := a.Audit(ctx, tx, nil, "company.signup", "company", cid, in.CompanyName+" signed up", nil); err != nil {
			return err
		}
		return a.NotifyStaff(ctx, tx, Note{Title: "New factory to approve: " + in.CompanyName, Body: in.Name + " (" + email + ") registered.", Link: "/admin/companies?status=pending", Email: true})
	})
	if err != nil {
		return nil, err
	}
	return a.UserByID(ctx, uid)
}

// InviteUser creates a user and emails them a link to set a password.
func (a *App) InviteUser(ctx context.Context, actor *User, companyID int64, role Role, name, email string) (*User, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, "", Errf("Enter the person's name.")
	}
	email, err := NormalizeEmail(email)
	if err != nil {
		return nil, "", err
	}
	switch role {
	case RoleAdmin, RoleOps, RoleViewer, RoleFactoryAdmin, RoleFactoryStaff:
	default:
		return nil, "", Errf("Choose a role.")
	}
	var uid int64
	var link string
	err = a.Tx(ctx, func(tx pgx.Tx) error {
		var exists bool
		_ = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE lower(email)=$1)`, email).Scan(&exists)
		if exists {
			return Errf("%s already has an account.", email)
		}
		var cid any
		if companyID != 0 {
			cid = companyID
		}
		if err := tx.QueryRow(ctx, `INSERT INTO users(company_id,role,name,email) VALUES ($1,$2,$3,$4) RETURNING id`, cid, role, name, email).Scan(&uid); err != nil {
			return err
		}
		tok, err := a.createLink(ctx, tx, "invite", uid, 7*24*time.Hour)
		if err != nil {
			return err
		}
		link = a.Cfg.BaseURL + "/invite?token=" + tok
		if _, err := tx.Exec(ctx, `INSERT INTO email_outbox(to_addr,subject,body) VALUES ($1,$2,$3)`, email, actor.Name+" invited you to Eimsky",
			"Hello "+name+",\n\n"+actor.Name+" added you to Eimsky as "+role.Label()+".\n\nSet your password here (the link works for 7 days):\n"+link); err != nil {
			return err
		}
		return a.Audit(ctx, tx, actor, "user.invite", "user", uid, fmt.Sprintf("Invited %s as %s", email, role.Label()), nil)
	})
	if err != nil {
		return nil, "", err
	}
	u, err := a.UserByID(ctx, uid)
	return u, link, err
}

// SetUserActive turns an account on or off and signs it out.
func (a *App) SetUserActive(ctx context.Context, actor *User, uid int64, active bool) error {
	if actor.ID == uid {
		return Errf("You can't deactivate your own account.")
	}
	return a.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE users SET active=$2 WHERE id=$1`, uid, active); err != nil {
			return err
		}
		if !active {
			if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, uid); err != nil {
				return err
			}
		}
		verb := "Deactivated"
		if active {
			verb = "Reactivated"
		}
		return a.Audit(ctx, tx, actor, "user.active", "user", uid, verb+" user", nil)
	})
}

// ---------- Phone OTP (forwarder app) ----------

func otpCode() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(1000000))
	return fmt.Sprintf("%06d", n.Int64())
}

// RequestOTP texts a 6-digit code. In dev mode the code is also returned so the
// app can be tested without an SMS provider.
func (a *App) RequestOTP(ctx context.Context, phone string) (normalized, devCode string, err error) {
	phone, err = NormalizePhone(phone)
	if err != nil {
		return "", "", err
	}
	var recent int
	_ = a.DB.QueryRow(ctx, `SELECT count(*) FROM one_time_tokens WHERE kind='otp' AND phone=$1 AND created_at > now() - interval '15 minutes'`, phone).Scan(&recent)
	if recent >= 5 {
		return "", "", Errf("Too many codes requested. Wait 15 minutes and try again.")
	}
	code := otpCode()
	if _, err := a.DB.Exec(ctx, `INSERT INTO one_time_tokens(kind,phone,token_hash,expires_at) VALUES ('otp',$1,$2,$3)`, phone, hashToken(phone+":"+code), a.Now().Add(10*time.Minute)); err != nil {
		return "", "", err
	}
	if err := a.SendSMS(ctx, phone, "Your Eimsky code is "+code+". It expires in 10 minutes."); err != nil {
		a.Log.Error("sms send failed", "err", err)
		if !a.Cfg.DevMode {
			return "", "", Errf("We couldn't send the text. Try again in a minute.")
		}
	}
	if a.Cfg.DevMode {
		devCode = code
	}
	return phone, devCode, nil
}

// VerifyOTP checks a code and returns the forwarder user for the phone,
// creating a new one if this is the first sign-in.
func (a *App) VerifyOTP(ctx context.Context, phone, code string) (*User, bool, error) {
	phone, err := NormalizePhone(phone)
	if err != nil {
		return nil, false, err
	}
	code = strings.TrimSpace(code)
	var id int64
	var hash string
	var attempts int
	err = a.DB.QueryRow(ctx, `SELECT id, token_hash, attempts FROM one_time_tokens
		WHERE kind='otp' AND phone=$1 AND used_at IS NULL AND expires_at > now() ORDER BY id DESC LIMIT 1`, phone).Scan(&id, &hash, &attempts)
	if err != nil {
		return nil, false, Errf("That code has expired. Ask for a new one.")
	}
	if attempts >= 5 {
		return nil, false, Errf("Too many wrong tries. Ask for a new code.")
	}
	if subtle.ConstantTimeCompare([]byte(hash), []byte(hashToken(phone+":"+code))) != 1 {
		_, _ = a.DB.Exec(ctx, `UPDATE one_time_tokens SET attempts=attempts+1 WHERE id=$1`, id)
		return nil, false, Errf("That code isn't right. Check the text and try again.")
	}
	var uid int64
	created := false
	err = a.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE one_time_tokens SET used_at=now() WHERE id=$1`, id); err != nil {
			return err
		}
		var role string
		err := tx.QueryRow(ctx, `SELECT id, role FROM users WHERE phone=$1`, phone).Scan(&uid, &role)
		if err == nil {
			if role != string(RoleForwarder) {
				return Errf("This number belongs to a web account. Use the Eimsky website to sign in.")
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		created = true
		return tx.QueryRow(ctx, `INSERT INTO users(role,name,phone) VALUES ('forwarder','',$1) RETURNING id`, phone).Scan(&uid)
	})
	if err != nil {
		return nil, false, err
	}
	u, err := a.UserByID(ctx, uid)
	if err != nil {
		return nil, false, err
	}
	if !u.Active {
		return nil, false, Errf("This account has been deactivated. Contact Eimsky.")
	}
	return u, created, nil
}

func (a *App) defaultSMS(ctx context.Context, to, msg string) error {
	if a.Cfg.SMSWebhookURL == "" {
		a.Log.Info("SMS (no provider configured)", "to", to, "message", msg)
		return nil
	}
	body, _ := json.Marshal(map[string]string{"to": to, "message": msg})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.Cfg.SMSWebhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if a.Cfg.SMSWebhookToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.Cfg.SMSWebhookToken)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("sms webhook returned %s", resp.Status)
	}
	return nil
}
