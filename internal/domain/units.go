// Package domain holds Eimsky's pure business logic: cargo measurement,
// loading rules, container planning, tender evaluation and billing.
// Nothing in this package touches the database or the network.
package domain

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

// Category is a cargo category a shipper picks at booking.
type Category string

const (
	CatFabric    Category = "fabric"
	CatTrims     Category = "trims"
	CatPackaging Category = "packaging"
	CatDyes      Category = "dyes"
	CatDG        Category = "dg"
)

// Categories lists every category in display order.
var Categories = []Category{CatFabric, CatTrims, CatPackaging, CatDyes, CatDG}

// BookableCategories are the categories a shipper may book in phase 1.
var BookableCategories = []Category{CatFabric, CatTrims, CatPackaging, CatDyes}

func (c Category) Label() string {
	switch c {
	case CatFabric:
		return "Fabric"
	case CatTrims:
		return "Trims and accessories"
	case CatPackaging:
		return "Packaging"
	case CatDyes:
		return "Dyes and chemicals (non-DG)"
	case CatDG:
		return "Dangerous goods (DG)"
	}
	return string(c)
}

func (c Category) Valid() bool {
	for _, x := range Categories {
		if x == c {
			return true
		}
	}
	return false
}

// Grade is a container's condition grade.
type Grade string

const (
	GradeA Grade = "A"
	GradeB Grade = "B"
	GradeC Grade = "C"
)

var Grades = []Grade{GradeA, GradeB, GradeC}

func (g Grade) Label() string {
	switch g {
	case GradeA:
		return "A · Clean, food grade"
	case GradeB:
		return "B · General cargo"
	case GradeC:
		return "C · Heavy, older box"
	}
	return string(g)
}

func (g Grade) Valid() bool { return g == GradeA || g == GradeB || g == GradeC }

// ContainerSpec describes a container type.
type ContainerSpec struct {
	Code        string  // 20GP, 40GP, 40HC, 45HC
	Name        string  // "High cube"
	InternalCBM float64 // physical internal volume
	PayloadKg   float64 // maximum payload
}

// UsableCBM is the space that can be sold, after the stowage factor.
func (s ContainerSpec) UsableCBM(stowage float64) float64 {
	return Round(s.InternalCBM*stowage, 1)
}

func (s ContainerSpec) Label() string { return s.Code + " · " + s.Name }

// DefaultContainerSpecs are the standard boxes Eimsky pools into.
var DefaultContainerSpecs = []ContainerSpec{
	{Code: "20GP", Name: "Dry", InternalCBM: 33.2, PayloadKg: 28000},
	{Code: "40GP", Name: "Dry", InternalCBM: 67.7, PayloadKg: 26500},
	{Code: "40HC", Name: "High cube", InternalCBM: 76.0, PayloadKg: 26000},
	{Code: "45HC", Name: "High cube", InternalCBM: 85.6, PayloadKg: 27500},
}

// SpecByCode finds a spec in a list.
func SpecByCode(specs []ContainerSpec, code string) (ContainerSpec, bool) {
	for _, s := range specs {
		if s.Code == code {
			return s, true
		}
	}
	return ContainerSpec{}, false
}

// Round rounds v to the given number of decimals.
func Round(v float64, decimals int) float64 {
	p := math.Pow(10, float64(decimals))
	return math.Round(v*p) / p
}

// CargoLine is one line of a booking: a number of identical cartons or rolls.
type CargoLine struct {
	HSCode   string
	Pieces   int
	LengthCm float64
	WidthCm  float64
	HeightCm float64
	KgEach   float64
}

// CBM is the volume of the line in cubic metres, rounded to 2 decimals.
func (l CargoLine) CBM() float64 {
	return Round(float64(l.Pieces)*l.LengthCm*l.WidthCm*l.HeightCm/1e6, 2)
}

// Kg is the total weight of the line.
func (l CargoLine) Kg() float64 { return Round(float64(l.Pieces)*l.KgEach, 1) }

// Validate checks a line has sensible values.
func (l CargoLine) Validate() error {
	if err := ValidateHSCode(l.HSCode); err != nil {
		return err
	}
	if l.Pieces <= 0 {
		return fmt.Errorf("enter how many cartons or rolls")
	}
	if l.LengthCm <= 0 || l.WidthCm <= 0 || l.HeightCm <= 0 {
		return fmt.Errorf("enter length, width and height in cm")
	}
	if l.LengthCm > 1200 || l.WidthCm > 235 || l.HeightCm > 270 {
		return fmt.Errorf("a piece of %.0f × %.0f × %.0f cm won't fit through a container door", l.LengthCm, l.WidthCm, l.HeightCm)
	}
	if l.KgEach <= 0 {
		return fmt.Errorf("enter the weight of each piece in kg")
	}
	return nil
}

// Totals sums a set of lines.
func Totals(lines []CargoLine) (pieces int, cbm, kg float64) {
	for _, l := range lines {
		pieces += l.Pieces
		cbm += l.CBM()
		kg += l.Kg()
	}
	return pieces, Round(cbm, 2), Round(kg, 1)
}

// WM is the chargeable weight-or-measure: the larger of CBM and tonnes.
func WM(cbm, kg float64) float64 {
	return Round(math.Max(cbm, kg/1000), 3)
}

var hsRe = regexp.MustCompile(`^\d{4}(\.\d{2}){1,3}$|^\d{4,10}$`)

// NormalizeHSCode turns "600632" into "6006.32" and trims spaces.
func NormalizeHSCode(code string) string {
	code = strings.TrimSpace(code)
	code = strings.ReplaceAll(code, " ", "")
	if !strings.Contains(code, ".") && len(code) >= 6 && len(code)%2 == 0 {
		digits := code
		out := digits[:4]
		for i := 4; i < len(digits); i += 2 {
			out += "." + digits[i:i+2]
		}
		return out
	}
	return code
}

// ValidateHSCode checks an HS code is at least 6 digits in a recognised format.
func ValidateHSCode(code string) error {
	c := NormalizeHSCode(code)
	if c == "" {
		return fmt.Errorf("enter an HS code")
	}
	if !hsRe.MatchString(c) || len(strings.ReplaceAll(c, ".", "")) < 6 {
		return fmt.Errorf("HS code %q should look like 6006.32 (at least 6 digits)", code)
	}
	ch := c[:2]
	if ch == "00" || ch > "97" {
		return fmt.Errorf("HS code %q has no chapter %s", code, ch)
	}
	return nil
}
