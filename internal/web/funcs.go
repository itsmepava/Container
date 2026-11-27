package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"strings"
	"time"

	"github.com/itsmepava/container/internal/app"
	"github.com/itsmepava/container/internal/domain"
)

// assetVersion busts browser caches when the server restarts with new CSS/JS.
var assetVersion = fmt.Sprint(time.Now().Unix())

type section struct{ Label, Href string }

var funcs = template.FuncMap{
	"assetVersion": func() string { return assetVersion },
	"pathIs":       func(path, want string) bool { return path == want },
	"sectionOf": func(path string) *section {
		switch {
		case strings.HasPrefix(path, "/f/bookings/"):
			return &section{"My bookings", "/f/bookings"}
		case strings.HasPrefix(path, "/ops/tenders/"):
			return &section{"Bids", "/ops/tenders"}
		case strings.HasPrefix(path, "/ops/bookings/"):
			return &section{"Bookings", "/ops/bookings"}
		case strings.HasPrefix(path, "/ops/sailings/"):
			return &section{"Load planner", "/ops/plan"}
		case strings.HasPrefix(path, "/admin/lanes/"), strings.HasPrefix(path, "/admin/sailings/"):
			return &section{"Lanes and sailings", "/admin/lanes"}
		case strings.HasPrefix(path, "/admin/companies/"):
			return &section{"Companies", "/admin/companies"}
		}
		return nil
	},
	"money":      domain.Money,
	"moneyShort": domain.MoneyShort,
	"moneyP": func(c *int64) string {
		if c == nil {
			return "–"
		}
		return domain.Money(*c)
	},
	"usd":  func(v float64) string { return domain.Money(domain.Cents(v)) },
	"usd0": func(v float64) string { return domain.Thousands(int64(math.Round(v))) },
	"usd1": func(v float64) string { return fmt.Sprintf("%.1f", v) },
	"usd2": func(v float64) string { return fmt.Sprintf("%.2f", v) },
	"thousands": func(v any) string {
		switch n := v.(type) {
		case int:
			return domain.Thousands(int64(n))
		case int64:
			return domain.Thousands(n)
		case float64:
			return domain.Thousands(int64(math.Round(n)))
		}
		return fmt.Sprint(v)
	},
	"n1": func(v float64) string { return fmt.Sprintf("%.1f", v) },
	"cbm": func(v float64) string { // up to 2 decimals, no trailing zeros: 6.6, 5.76, 12
		s := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", v), "0"), ".")
		if s == "" || s == "-0" {
			return "0"
		}
		return s
	},
	"n2":  func(v float64) string { return fmt.Sprintf("%.2f", v) },
	"n0":  func(v float64) string { return fmt.Sprintf("%.0f", v) },
	"pct": func(v float64) string { return fmt.Sprintf("%.0f%%", v) },
	"pctw": func(v float64) string { // CSS width, clamped
		return fmt.Sprintf("%.2f%%", math.Max(0, math.Min(100, v)))
	},
	"tonnes": func(kg float64) string { return fmt.Sprintf("%.1f", kg/1000) },
	"div": func(a, b float64) float64 {
		if b == 0 {
			return 0
		}
		return a / b
	},
	"mul":  func(a, b float64) float64 { return a * b },
	"sub":  func(a, b float64) float64 { return a - b },
	"add":  func(a, b int) int { return a + b },
	"addf": func(a, b float64) float64 { return a + b },
	"float": func(v any) float64 {
		switch n := v.(type) {
		case int:
			return float64(n)
		case int64:
			return float64(n)
		case float64:
			return n
		}
		return 0
	},
	"cents2f": func(c int64) float64 { return float64(c) / 100 },
	"neg":     func(v float64) bool { return v < 0 },
	"dt": func(t time.Time) string {
		if t.IsZero() {
			return "–"
		}
		return t.In(domain.Colombo).Format("Mon 2 Jan 15:04")
	},
	"dtp": func(t *time.Time) string {
		if t == nil || t.IsZero() {
			return "–"
		}
		return t.In(domain.Colombo).Format("Mon 2 Jan 15:04")
	},
	"day": func(t time.Time) string {
		if t.IsZero() {
			return "–"
		}
		return t.In(domain.Colombo).Format("Mon 2 Jan")
	},
	"date": func(t time.Time) string { return t.In(domain.Colombo).Format("2 Jan 2006") },
	"hm":   func(t time.Time) string { return t.In(domain.Colombo).Format("2 Jan 15:04") },
	"iso":  func(t time.Time) string { return t.UTC().Format(time.RFC3339) },
	"isop": func(t *time.Time) string {
		if t == nil {
			return ""
		}
		return t.UTC().Format(time.RFC3339)
	},
	"local": func(t time.Time) string { // for datetime-local inputs
		if t.IsZero() {
			return ""
		}
		return t.In(domain.Colombo).Format("2006-01-02T15:04")
	},
	"ago": func(t time.Time) string {
		d := time.Since(t)
		switch {
		case d < time.Minute:
			return "just now"
		case d < time.Hour:
			return fmt.Sprintf("%d min ago", int(d.Minutes()))
		case d < 24*time.Hour:
			return fmt.Sprintf("%d h ago", int(d.Hours()))
		case d < 7*24*time.Hour:
			return fmt.Sprintf("%d d ago", int(d.Hours()/24))
		}
		return t.In(domain.Colombo).Format("2 Jan")
	},
	"until": func(t time.Time) string { return domain.Until(time.Now(), t) },
	"past":  func(t time.Time) bool { return time.Now().After(t) },
	"soon":  func(t time.Time, hours int) bool { return time.Until(t) < time.Duration(hours)*time.Hour },
	"lot":   app.LotLabel,
	"cat": func(c any) string {
		switch v := c.(type) {
		case domain.Category:
			return v.Label()
		case string:
			return domain.Category(v).Label()
		}
		return fmt.Sprint(c)
	},
	"categories":    func() []domain.Category { return domain.BookableCategories },
	"allCategories": func() []domain.Category { return domain.Categories },
	"grades":        func() []domain.Grade { return domain.Grades },
	"inclusions":    func() []domain.Inclusion { return domain.Inclusions },
	"moveReasons":   func() []string { return app.MoveReasons },
	"docLabel":      app.DocKindLabel,
	"upper":         strings.ToUpper,
	"lower":         strings.ToLower,
	"title": func(s string) string {
		if s == "" {
			return s
		}
		return strings.ToUpper(s[:1]) + s[1:]
	},
	"join": strings.Join,
	"has":  strings.Contains,
	"eq64": func(a, b int64) bool { return a == b },
	"seq": func(n int) []int {
		s := make([]int, n)
		for i := range s {
			s[i] = i
		}
		return s
	},
	"dict": func(kv ...any) map[string]any {
		m := map[string]any{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[fmt.Sprint(kv[i])] = kv[i+1]
		}
		return m
	},
	"json": func(v any) template.JS {
		b, _ := json.Marshal(v)
		return template.JS(b)
	},
	"deref": func(p *int64) int64 {
		if p == nil {
			return 0
		}
		return *p
	},
	"inIDs": func(id int64, ids []int64) bool {
		for _, x := range ids {
			if x == id {
				return true
			}
		}
		return false
	},
	"plural": func(n int, one, many string) string {
		if n == 1 {
			return fmt.Sprintf("%d %s", n, one)
		}
		return fmt.Sprintf("%d %s", n, many)
	},
	"badgeClass": func(b string) string {
		switch b {
		case "DECIDE", "AWARDED":
			return "green"
		case "CO-LOAD":
			return "amber"
		case "BIDDING":
			return "blue"
		}
		return ""
	},
	"statusClass": func(s string) string {
		switch s {
		case "open", "Open", "Booked", "approved", "issued", "Open for booking":
			return "blue"
		case "awarded", "Awarded", "Priced", "paid", "won", "booked":
			return "green"
		case "closed", "Bids closed", "pending", "Waiting to place", "Held", "Co-load", "Forwarders bidding":
			return "amber"
		case "cancelled", "Cancelled", "rejected", "suspended", "expired", "Hold expired", "lost", "void":
			return "red"
		}
		return ""
	},
	"lotWidth": func(cbm, usable float64) string {
		if usable <= 0 {
			return "0"
		}
		return fmt.Sprintf("%.3f%%", cbm/usable*100)
	},
	"safeURL":   func(s string) template.URL { return template.URL(s) },
	"emptyLine": func() domain.CargoLine { return domain.CargoLine{} },
	"docNames": func(short []string) string {
		var out []string
		for _, x := range short {
			switch x {
			case "Invoice":
				out = append(out, "commercial invoice")
			case "Licence":
				out = append(out, "import licence")
			default:
				out = append(out, strings.ToLower(x))
			}
		}
		return strings.Join(out, " and ")
	},
	"add64":  func(a, b int64) int64 { return a + b },
	"list2":  func(a, b any) []any { return []any{a, b} },
	"list2s": func(a, b string) []string { return []string{a, b} },
	"sub64":  func(a, b int64) int64 { return a - b },
}
