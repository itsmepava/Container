package web

import (
	"fmt"
	"html"
	"html/template"
	"strings"

	"github.com/itsmepava/container/internal/app"
	"github.com/itsmepava/container/internal/domain"
)

// svgLot is one shipper's slice of a container drawing.
type svgLot struct {
	ID    int64
	Name  string // shown on wide slices
	CBM   float64
	Color int  // lot-N palette index; -1 = neutral (another shipper)
	You   bool // the viewer's own lot (shipper view)
}

// svgBox describes a container to draw in oblique 3D, to true scale.
type svgBox struct {
	Spec      domain.ContainerSpec
	Usable    float64 // usable CBM (stowage applied)
	BreakEven float64 // CBM at which an own box beats co-load; 0 = none
	Lots      []svgLot
	MaxLen    float64 // longest box on the page, so boxes compare to scale
}

const svgScale = 64.0 // viewBox units per metre

// boxSVG draws the container: a front face with lots as slices along its
// length (usable space spread over the full length), an oblique top, the door
// end, free space, and the break-even and usable markers.
func boxSVG(b svgBox) template.HTML {
	l, w, h := b.Spec.Dims()
	maxL := b.MaxLen
	if maxL < l {
		maxL = l
	}
	s := svgScale
	dx, dy := w*s*0.42, w*s*0.28
	x0, y0 := 10.0, dy+12
	Ls, Hs := l*s, h*s
	vbW := maxL*s + dx + 24
	vbH := y0 + Hs + 44
	perCBM := 0.0
	if b.Usable > 0 {
		perCBM = Ls / b.Usable
	}
	var g strings.Builder
	f := func(format string, a ...any) { fmt.Fprintf(&g, format, a...) }

	// Body: top face and front face.
	f(`<polygon class="bx-top" points="%.1f,%.1f %.1f,%.1f %.1f,%.1f %.1f,%.1f"/>`, x0, y0, x0+Ls, y0, x0+Ls+dx, y0-dy, x0+dx, y0-dy)
	f(`<rect class="bx-front" x="%.1f" y="%.1f" width="%.1f" height="%.1f"/>`, x0, y0, Ls, Hs)
	for m := 0.3; m < l; m += 0.3 {
		f(`<line class="bx-rib" x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f"/>`, x0+m*s, x0+m*s, y0+2, y0+Hs-2)
	}
	// Lots.
	a := 0.0
	used := 0.0
	for _, lot := range b.Lots {
		ln := lot.CBM * perCBM
		if a+ln > Ls {
			ln = Ls - a
		}
		if ln <= 0 {
			continue
		}
		x := x0 + a
		fill := fmt.Sprintf("var(--lot%d)", lot.Color)
		cls := "bx-lot"
		if lot.Color < 0 {
			fill = "var(--line-strong)"
			cls += " other"
		}
		if lot.You {
			fill = "var(--brand)"
		}
		f(`<g class="%s" data-lot="%d"><title>%s · %.1f CBM</title>`, cls, lot.ID, html.EscapeString(lot.Name), lot.CBM)
		f(`<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="%s" stroke="var(--surface)"/>`, x, y0, ln, Hs, fill)
		f(`<polygon points="%.1f,%.1f %.1f,%.1f %.1f,%.1f %.1f,%.1f" fill="%s" opacity=".72" stroke="var(--surface)"/>`, x, y0, x+ln, y0, x+ln+dx, y0-dy, x+dx, y0-dy, fill)
		txt := "#fff"
		if lot.Color < 0 {
			txt = "var(--ink-2)"
		}
		name := fitText(lot.Name, int(ln/7.2))
		switch {
		case ln > 70:
			f(`<text x="%.1f" y="%.1f" text-anchor="middle" fill="%s" class="bx-name">%s</text>`, x+ln/2, y0+Hs/2-5, txt, html.EscapeString(name))
			f(`<text x="%.1f" y="%.1f" text-anchor="middle" fill="%s" class="bx-num">%.1f</text>`, x+ln/2, y0+Hs/2+13, txt, lot.CBM)
		case ln > 26:
			f(`<text x="%.1f" y="%.1f" text-anchor="middle" fill="%s" class="bx-num">%.1f</text>`, x+ln/2, y0+Hs/2+4, txt, lot.CBM)
		}
		g.WriteString(`</g>`)
		a += ln
		used += lot.CBM
	}
	// Door end.
	f(`<polygon class="bx-end" points="%.1f,%.1f %.1f,%.1f %.1f,%.1f %.1f,%.1f"/>`, x0+Ls, y0, x0+Ls+dx, y0-dy, x0+Ls+dx, y0-dy+Hs, x0+Ls, y0+Hs)
	f(`<line class="bx-door" x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f"/>`, x0+Ls+dx*.5, x0+Ls+dx*.5, y0-dy*.5+6, y0-dy*.5+Hs-6)
	// Free space.
	if a < Ls-8 {
		f(`<rect class="bx-free" x="%.1f" y="%.1f" width="%.1f" height="%.1f"/>`, x0+a+2, y0+3, Ls-a-4, Hs-6)
		if Ls-a > 110 {
			free := b.Usable - used
			if free < 0 {
				free = 0
			}
			f(`<text class="bx-freetxt" x="%.1f" y="%.1f" text-anchor="middle">%.1f CBM free</text>`, x0+a+(Ls-a)/2, y0+Hs/2+4, free)
		}
	}
	// Markers.
	if b.BreakEven > 0 && b.BreakEven < b.Usable {
		bx := x0 + b.BreakEven*perCBM
		f(`<line class="bx-be" x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f"/>`, bx, bx, y0-6, y0+Hs+6)
		ty := y0 + Hs + 20
		if x0+Ls-bx < 180 {
			ty += 15 // keep clear of the "usable" label
		}
		f(`<text class="bx-betxt" x="%.1f" y="%.1f" text-anchor="middle">break-even %.1f</text>`, bx, ty, b.BreakEven)
	}
	f(`<text class="bx-usetxt" x="%.1f" y="%.1f" text-anchor="end">usable %.1f CBM</text>`, x0+Ls, y0+Hs+20, b.Usable)

	return template.HTML(fmt.Sprintf(`<svg class="box3d" viewBox="0 0 %.0f %.0f" role="img" aria-label="%s, %.1f of %.1f usable CBM loaded" preserveAspectRatio="xMinYMid meet">%s</svg>`,
		vbW, vbH, b.Spec.Code, used, b.Usable, g.String()))
}

func specFor(code string, specs []domain.ContainerSpec) domain.ContainerSpec {
	if s, ok := domain.SpecByCode(specs, code); ok {
		return s
	}
	if s, ok := domain.SpecByCode(domain.DefaultContainerSpecs, code); ok {
		return s
	}
	return domain.ContainerSpec{Code: code, InternalCBM: 67.7}
}

func init() {
	// box3d draws a planning container with its placed lots.
	funcs["box3d"] = func(pb *app.PlannedBox, p *app.Planner) template.HTML {
		b := svgBox{Spec: pb.Container.Spec, Usable: pb.Box.Usable, BreakEven: p.BreakEvenFor(pb.Container.BoxCode), MaxLen: p.MaxLen()}
		for _, l := range pb.Lots {
			b.Lots = append(b.Lots, svgLot{ID: l.Booking.ID, Name: l.Label + " " + l.Booking.CompanyName, CBM: l.Booking.CBM, Color: l.Color})
		}
		return boxSVG(b)
	}
	// box3dOption draws the simulated load of a container option.
	funcs["box3dOption"] = func(o domain.ContainerOption, p *app.Planner) template.HTML {
		b := svgBox{Spec: o.Spec, Usable: o.Usable, BreakEven: p.BreakEvenFor(o.Spec.Code), MaxLen: p.MaxLen()}
		if o.Box != nil {
			for _, l := range o.Box.Lots {
				name := l.Label + " " + l.Name
				b.Lots = append(b.Lots, svgLot{ID: l.ID, Name: name, CBM: l.CBM, Color: p.ColorOf[l.ID]})
			}
		}
		return boxSVG(b)
	}
	// box3dMine draws a shipper's anonymous view: their lot, everyone else, free space.
	funcs["box3dMine"] = func(code string, usable, mine, others float64) template.HTML {
		b := svgBox{Spec: specFor(code, nil), Usable: usable}
		b.Lots = append(b.Lots, svgLot{Name: "You", CBM: mine, You: true})
		if others > 0 {
			b.Lots = append(b.Lots, svgLot{Name: "Other shippers", CBM: others, Color: -1})
		}
		return boxSVG(b)
	}
	funcs["dims"] = func(sp domain.ContainerSpec) string {
		l, w, h := sp.Dims()
		return fmt.Sprintf("%.2f × %.2f × %.2f m", l, w, h)
	}
	funcs["unplacedGroups"] = domain.GroupUnplaced
	funcs["rulesTable"] = func() []domain.Category { return domain.Categories }
}

// fitText shortens s to at most n characters, on a word boundary when it can.
func fitText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n < 2 {
		return ""
	}
	cut := string(r[:n-1])
	if i := strings.LastIndexByte(cut, ' '); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ·,") + "…"
}
