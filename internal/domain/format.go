package domain

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func fmtInt(v float64) string { return Thousands(int64(v + 0.5)) }
func fmtPct(v float64) string { return fmt.Sprintf("%.0f%%", v) }
func fmtNum(v float64) string { return strconv.FormatFloat(Round(v, 1), 'f', 1, 64) }

// Thousands formats an integer with comma separators.
func Thousands(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// Money formats cents as "2,350.00".
func Money(cents int64) string {
	neg := cents < 0
	if neg {
		cents = -cents
	}
	s := Thousands(cents/100) + fmt.Sprintf(".%02d", cents%100)
	if neg {
		return "-" + s
	}
	return s
}

// MoneyShort formats cents without decimals when they are zero: "2,350" or "320.21".
func MoneyShort(cents int64) string {
	if cents%100 == 0 {
		return Thousands(cents / 100)
	}
	return Money(cents)
}

// Colombo is Sri Lanka's time zone; all times are shown in it.
var Colombo = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Colombo")
	if err != nil {
		return time.FixedZone("IST", 5*3600+1800)
	}
	return loc
}()

// ISOWeek returns the ISO year and week of t in Colombo time.
func ISOWeek(t time.Time) (int, int) { return t.In(Colombo).ISOWeek() }

// WeekStart returns Monday 00:00 Colombo of the given ISO week.
func WeekStart(year, week int) time.Time {
	jan4 := time.Date(year, 1, 4, 0, 0, 0, 0, Colombo)
	wd := int(jan4.Weekday())
	if wd == 0 {
		wd = 7
	}
	monday := jan4.AddDate(0, 0, 1-wd)
	return monday.AddDate(0, 0, (week-1)*7)
}

// Until describes a duration until a deadline: "5 d left", "1 d 6 h", "40 min".
func Until(now, t time.Time) string {
	d := t.Sub(now)
	if d <= 0 {
		return "passed"
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	switch {
	case days >= 2:
		return fmt.Sprintf("%d d", days)
	case days >= 1:
		return fmt.Sprintf("%d d %d h", days, hours)
	case hours >= 1:
		return fmt.Sprintf("%d h %d min", hours, mins)
	default:
		return fmt.Sprintf("%d min", mins+1)
	}
}
