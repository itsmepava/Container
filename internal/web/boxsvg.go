package web

import (
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"math"
	"strings"
	"time"

	"github.com/itsmepava/container/internal/app"
	"github.com/itsmepava/container/internal/domain"
)

// figLot is one slice in a container drawing.
type figLot struct {
	ID   int64   `json:"id"`
	Name string  `json:"n"`
	CBM  float64 `json:"c"`
	Col  string  `json:"col"`
	Ink  string  `json:"ink,omitempty"` // text colour; white when empty
}

// figBox is what app.js needs to draw a container in oblique 3D, to scale,
// exactly as the load planner does: lots laid along the length, free space,
// the break-even line and the usable label.
type figBox struct {
	Code      string   `json:"t"`
	L         float64  `json:"L"`
	W         float64  `json:"W"`
	H         float64  `json:"H"`
	Usable    float64  `json:"usable"`
	BreakEven float64  `json:"be,omitempty"` // CBM / W/M; 0 = none
	MaxL      float64  `json:"maxL"`
	Lots      []figLot `json:"lots"`
}

func figHTML(b figBox, label string) template.HTML {
	if b.MaxL < b.L {
		b.MaxL = b.L
	}
	data, _ := json.Marshal(b)
	return template.HTML(fmt.Sprintf(`<div class="fig" data-box="%s"><svg role="img" aria-label="%s"></svg></div>`,
		html.EscapeString(string(data)), html.EscapeString(label)))
}

func newFig(spec domain.ContainerSpec, usable, be, maxL float64) figBox {
	l, w, h := spec.Dims()
	if math.IsInf(be, 0) || math.IsNaN(be) || be <= 0 {
		be = 0
	}
	return figBox{Code: spec.Code, L: l, W: w, H: h, Usable: usable, BreakEven: domain.Round(be, 2), MaxL: maxL}
}

func specFor(code string) domain.ContainerSpec {
	if s, ok := domain.SpecByCode(domain.DefaultContainerSpecs, code); ok {
		return s
	}
	return domain.ContainerSpec{Code: code, InternalCBM: 67.7}
}

func init() {
	// fleetFig draws a container option's simulated load.
	funcs["fleetFig"] = func(fp domain.FleetPlan, p *app.Planner) template.HTML {
		b := newFig(fp.Option.Spec, fp.Usable, fp.BreakEven, p.FleetMaxLen())
		for _, l := range fp.Fit {
			b.Lots = append(b.Lots, figLot{ID: l.ID, Name: l.Name, CBM: l.CBM, Col: p.HueOf[l.ID]})
		}
		return figHTML(b, fmt.Sprintf("%s grade %s filled %.0f percent", fp.Option.Spec.Code, fp.Option.Grade, fp.Fill*100))
	}
	// boxFig draws a planning container with the lots placed in it.
	funcs["boxFig"] = func(pb *app.PlannedBox, p *app.Planner) template.HTML {
		be := 0.0
		if u := p.Sell - p.Handling; u > 0 {
			be = p.Sailing.Lane.Rates[pb.Container.BoxCode] / u
		}
		b := newFig(pb.Container.Spec, pb.Box.Usable, be, p.MaxLen())
		for _, l := range pb.Lots {
			b.Lots = append(b.Lots, figLot{ID: l.Booking.ID, Name: l.Booking.CompanyName, CBM: l.Booking.CBM, Col: l.Hue})
		}
		return figHTML(b, fmt.Sprintf("Container %d, %s, %.1f of %.1f usable CBM loaded", pb.Container.Seq, pb.Container.BoxCode, pb.Box.UsedCBM(), pb.Box.Usable))
	}
	// mineFig is a shipper's anonymous view: their lot, everyone else, free space.
	funcs["mineFig"] = func(code string, usable, mine, others float64) template.HTML {
		b := newFig(specFor(code), usable, 0, 0)
		b.Lots = append(b.Lots, figLot{Name: "You", CBM: mine, Col: "#127a52"})
		if others > 0 {
			b.Lots = append(b.Lots, figLot{Name: "Other shippers", CBM: others, Col: "#c3cdd8", Ink: "#0d1b2a"})
		}
		return figHTML(b, fmt.Sprintf("Your %.1f CBM in a %s", mine, code))
	}
	funcs["dims"] = func(sp domain.ContainerSpec) string {
		l, w, h := sp.Dims()
		return fmt.Sprintf("%g × %g × %g m", l, w, h)
	}
	funcs["rulesTable"] = func() []domain.Category { return domain.Categories }
	funcs["pct100"] = func(v float64) string { return fmt.Sprintf("%.0f%%", v*100) }
	funcs["usdSigned"] = func(v float64) string {
		s := "USD " + thousandsInt(math.Abs(math.Round(v)))
		if v < 0 {
			return "− " + s
		}
		return s
	}
	// sailLabel is "Thu 10 Dec · MV Kelani Star 050W".
	funcs["sailLabel"] = func(s *app.Sailing) string {
		if s == nil {
			return "no sailing"
		}
		v := s.Vessel()
		if v == nil {
			return s.Label()
		}
		return strings.TrimSpace(v.ETD.In(domain.Colombo).Format("Mon 2 Jan") + " · " + v.Name + " " + v.Voyage)
	}
	// leftTxt is the time to the CFS cut-off, as the sailing cards show it.
	funcs["leftTxt"] = func(s *app.Sailing) string {
		now := time.Now()
		if v := s.Vessel(); v != nil && v.ETD.Before(now) {
			return "sailed"
		}
		ms := s.CFSCutoff.Sub(now)
		if ms <= 0 {
			return "cut-off passed"
		}
		d, h := int(ms.Hours())/24, int(ms.Hours())%24
		if d > 0 {
			return fmt.Sprintf("%d d %d h to CFS cut-off", d, h)
		}
		return fmt.Sprintf("%d h to CFS cut-off", h)
	}
	funcs["cutTxt"] = func(t time.Time) string { return t.In(domain.Colombo).Format("Mon 2 Jan 15:04") }
	funcs["isInf"] = func(v float64) bool { return math.IsInf(v, 0) }
}

func thousandsInt(v float64) string {
	s := fmt.Sprintf("%.0f", v)
	out := ""
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out += ","
		}
		out += string(c)
	}
	return out
}
