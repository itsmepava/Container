package web

import (
	"strings"
	"testing"

	"github.com/itsmepava/container/internal/domain"
)

func TestBoxSVG(t *testing.T) {
	spec, _ := domain.SpecByCode(domain.DefaultContainerSpecs, "40HC")
	usable := spec.UsableCBM(0.85)
	// Nearly full box: free space is a sliver and must not produce negative sizes.
	out := string(boxSVG(svgBox{Spec: spec, Usable: usable, BreakEven: 42.7, Lots: []svgLot{
		{ID: 1, Name: "A Ceylon <Weave> Mills", CBM: 30, Color: 0},
		{ID: 2, Name: "B Kandy", CBM: usable - 30.05, Color: 1},
	}}))
	if strings.Contains(out, `width="-`) || strings.Contains(out, `height="-`) {
		t.Fatalf("negative size in %s", out)
	}
	for _, want := range []string{`data-lot="1"`, `data-lot="2"`, "break-even 42.7", "&lt;Weave&gt;", "usable 64.6 CBM"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(out, "<Weave>") {
		t.Error("lot name not escaped")
	}
	if got := fitText("Ceylon Weave Mills", 10); got != "Ceylon…" {
		t.Errorf("fitText = %q", got)
	}
}
