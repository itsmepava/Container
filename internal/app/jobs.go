package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/itsmepava/container/internal/domain"
)

// RunJobs runs the background jobs every interval until ctx ends. Only one
// instance runs a tick at a time (Postgres advisory lock).
func (a *App) RunJobs(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		a.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick runs each job once.
func (a *App) Tick(ctx context.Context) {
	conn, err := a.DB.Acquire(ctx)
	if err != nil {
		a.Log.Error("jobs: acquire", "err", err)
		return
	}
	defer conn.Release()
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(424242)`).Scan(&got); err != nil || !got {
		return
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock(424242)`)

	run := func(name string, f func(context.Context) (int, error)) {
		n, err := f(ctx)
		if err != nil {
			a.Log.Error("job failed", "job", name, "err", err)
		} else if n > 0 {
			a.Log.Info("job", "job", name, "count", n)
		}
	}
	run("expire_holds", func(ctx context.Context) (int, error) { n, err := a.ExpireHolds(ctx); return int(n), err })
	run("close_tenders", a.CloseDueTenders)
	run("doc_reminders", a.SendDocReminders)
	run("tender_reminders", a.SendTenderReminders)
	run("issue_invoices", a.IssueDueInvoices)
	run("purge_documents", a.PurgeDocuments)
	run("deliver_email", a.DeliverEmail)
	run("deliver_push", a.DeliverPush)
}

// SendDocReminders reminds shippers of missing documents before the CFS cut-off.
func (a *App) SendDocReminders(ctx context.Context) (int, error) {
	st := a.Settings(ctx)
	bs, err := a.queryBookings(ctx, a.DB, `b.status='booked' AND EXISTS (SELECT 1 FROM sailings s WHERE s.id=b.sailing_id AND s.cfs_cutoff > $1 AND s.cfs_cutoff <= $2 AND s.status NOT IN ('cancelled','closed'))`,
		a.Now(), a.Now().Add(time.Duration(st.ReminderHours)*time.Hour))
	if err != nil {
		return 0, err
	}
	missing, err := a.MissingDocs(ctx, bs)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, b := range bs {
		m := missing[b.ID]
		if len(m) == 0 {
			continue
		}
		fresh, err := a.Mark(ctx, a.DB, fmt.Sprintf("docremind:%d", b.ID))
		if err != nil || !fresh {
			continue
		}
		s, err := a.SailingByID(ctx, b.SailingID)
		if err != nil {
			continue
		}
		var labels []string
		for _, x := range m {
			if x == "Invoice" {
				labels = append(labels, "commercial invoice")
			} else {
				labels = append(labels, "import licence")
			}
		}
		_ = a.NotifyCompany(ctx, a.DB, b.CompanyID, Note{Title: "Documents due for " + b.ref(),
			Body: fmt.Sprintf("Please upload the %s before the CFS cut-off, %s (%s left).", strings.Join(labels, " and "), fmtTime(s.CFSCutoff), domain.Until(a.Now(), s.CFSCutoff)),
			Link: fmt.Sprintf("/f/bookings/%d", b.ID), Email: true})
		n++
	}
	return n, nil
}

func (b *Booking) ref() string { return b.Ref }

// SendTenderReminders nudges invited forwarders who haven't bid, two hours before close.
func (a *App) SendTenderReminders(ctx context.Context) (int, error) {
	rows, err := a.DB.Query(ctx, `SELECT t.id, t.ref, t.closes_at FROM tenders t WHERE t.status='open' AND t.closes_at > $1 AND t.closes_at <= $2`, a.Now(), a.Now().Add(2*time.Hour))
	if err != nil {
		return 0, err
	}
	type tr struct {
		id    int64
		ref   string
		close time.Time
	}
	var ts []tr
	for rows.Next() {
		var t tr
		_ = rows.Scan(&t.id, &t.ref, &t.close)
		ts = append(ts, t)
	}
	rows.Close()
	n := 0
	for _, t := range ts {
		fresh, err := a.Mark(ctx, a.DB, fmt.Sprintf("tenderremind:%d", t.id))
		if err != nil || !fresh {
			continue
		}
		ids, err := usersOf(ctx, a.DB, `company_id IN (SELECT company_id FROM tender_invites WHERE tender_id=$1 EXCEPT SELECT company_id FROM bids WHERE tender_id=$1)`, t.id)
		if err != nil {
			continue
		}
		_ = a.Notify(ctx, a.DB, ids, Note{Title: t.ref + " closes in " + domain.Until(a.Now(), t.close), Body: "You haven't bid yet.", Push: true, Data: map[string]any{"tenderId": t.id}})
		n += len(ids)
	}
	return n, nil
}

// OutboxEmail is a queued email (shown on /dev/mail in development).
type OutboxEmail struct {
	ID        int64
	To        string
	Subject   string
	Body      string
	CreatedAt time.Time
	SentAt    *time.Time
	LastError string
}

func (a *App) Outbox(ctx context.Context, limit int) ([]OutboxEmail, error) {
	rows, err := a.DB.Query(ctx, `SELECT id,to_addr,subject,body,created_at,sent_at,last_error FROM email_outbox ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OutboxEmail
	for rows.Next() {
		var e OutboxEmail
		if err := rows.Scan(&e.ID, &e.To, &e.Subject, &e.Body, &e.CreatedAt, &e.SentAt, &e.LastError); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// DeliverEmail sends queued emails by SMTP. Without SMTP configured they are
// logged and marked sent, and stay readable on /dev/mail in dev mode.
func (a *App) DeliverEmail(ctx context.Context) (int, error) {
	es, err := a.pendingEmails(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range es {
		var sendErr error
		if a.Cfg.SMTPHost != "" {
			sendErr = a.sendSMTP(e.To, e.Subject, e.Body)
		} else {
			a.Log.Info("email (no SMTP configured)", "to", e.To, "subject", e.Subject)
		}
		if sendErr != nil {
			_, _ = a.DB.Exec(ctx, `UPDATE email_outbox SET attempts=attempts+1, last_error=$2 WHERE id=$1`, e.ID, sendErr.Error())
			continue
		}
		_, _ = a.DB.Exec(ctx, `UPDATE email_outbox SET sent_at=now(), attempts=attempts+1 WHERE id=$1`, e.ID)
		n++
	}
	return n, nil
}

func (a *App) pendingEmails(ctx context.Context) ([]OutboxEmail, error) {
	rows, err := a.DB.Query(ctx, `SELECT id,to_addr,subject,body,created_at,sent_at,last_error FROM email_outbox WHERE sent_at IS NULL AND attempts < 8 ORDER BY id LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OutboxEmail
	for rows.Next() {
		var e OutboxEmail
		if err := rows.Scan(&e.ID, &e.To, &e.Subject, &e.Body, &e.CreatedAt, &e.SentAt, &e.LastError); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (a *App) sendSMTP(to, subject, body string) error {
	from := a.Cfg.SMTPFrom
	addr := a.Cfg.SMTPFrom
	if i := strings.Index(from, "<"); i >= 0 {
		addr = strings.Trim(from[i:], "<> ")
	}
	msg := "From: " + from + "\r\nTo: " + to + "\r\nSubject: " + mimeSubject(subject) +
		"\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" + strings.ReplaceAll(body, "\n", "\r\n")
	var auth smtp.Auth
	if a.Cfg.SMTPUser != "" {
		auth = smtp.PlainAuth("", a.Cfg.SMTPUser, a.Cfg.SMTPPass, a.Cfg.SMTPHost)
	}
	return smtp.SendMail(net.JoinHostPort(a.Cfg.SMTPHost, strconv.Itoa(a.Cfg.SMTPPort)), auth, addr, []string{to}, []byte(msg))
}

func mimeSubject(s string) string {
	for _, r := range s {
		if r > 127 {
			return "=?UTF-8?B?" + b64(s) + "?="
		}
	}
	return s
}

// DeliverPush sends queued push notifications through Expo's push service.
func (a *App) DeliverPush(ctx context.Context) (int, error) {
	rows, err := a.DB.Query(ctx, `SELECT p.id, p.title, p.body, p.data::text, t.token FROM push_outbox p JOIN push_tokens t ON t.user_id=p.user_id
		WHERE p.sent_at IS NULL AND p.attempts < 5 ORDER BY p.id LIMIT 100`)
	if err != nil {
		return 0, err
	}
	type msg struct {
		To    string          `json:"to"`
		Title string          `json:"title"`
		Body  string          `json:"body"`
		Data  json.RawMessage `json:"data"`
		Sound string          `json:"sound"`
	}
	var msgs []msg
	var ids []int64
	seen := map[int64]bool{}
	for rows.Next() {
		var id int64
		var m msg
		var data string
		if err := rows.Scan(&id, &m.Title, &m.Body, &data, &m.To); err != nil {
			rows.Close()
			return 0, err
		}
		m.Data, m.Sound = json.RawMessage(data), "default"
		msgs = append(msgs, m)
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	rows.Close()
	// Rows whose user has no token any more are dropped.
	_, _ = a.DB.Exec(ctx, `UPDATE push_outbox SET sent_at=now(), last_error='no device' WHERE sent_at IS NULL AND NOT EXISTS (SELECT 1 FROM push_tokens WHERE user_id=push_outbox.user_id)`)
	if len(msgs) == 0 {
		return 0, nil
	}
	if !a.Cfg.PushEnabled {
		_, _ = a.DB.Exec(ctx, `UPDATE push_outbox SET sent_at=now(), last_error='push disabled' WHERE id = ANY($1)`, ids)
		return 0, nil
	}
	body, _ := json.Marshal(msgs)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, a.Cfg.ExpoPushURL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if a.Cfg.ExpoAccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.Cfg.ExpoAccessToken)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		_, _ = a.DB.Exec(ctx, `UPDATE push_outbox SET attempts=attempts+1, last_error=$2 WHERE id = ANY($1)`, ids, err.Error())
		return 0, nil
	}
	defer resp.Body.Close()
	var out struct {
		Data []struct {
			Status  string `json:"status"`
			Details struct {
				Error string `json:"error"`
			} `json:"details"`
		} `json:"data"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	for i, d := range out.Data {
		if i < len(msgs) && d.Details.Error == "DeviceNotRegistered" {
			_, _ = a.DB.Exec(ctx, `DELETE FROM push_tokens WHERE token=$1`, msgs[i].To)
		}
	}
	if resp.StatusCode >= 300 {
		_, _ = a.DB.Exec(ctx, `UPDATE push_outbox SET attempts=attempts+1, last_error=$2 WHERE id = ANY($1)`, ids, resp.Status)
		return 0, nil
	}
	_, _ = a.DB.Exec(ctx, `UPDATE push_outbox SET sent_at=now(), attempts=attempts+1 WHERE id = ANY($1)`, ids)
	return len(msgs), nil
}
