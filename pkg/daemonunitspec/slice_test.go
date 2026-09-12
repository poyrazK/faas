package daemonunitspec

// spec: §13

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/daemonunit"
)

func TestUnitSlice_Renders(t *testing.T) {
	u := UnitSlice()
	if u.Slice != "faas-cp.slice" {
		t.Errorf("Slice = %q, want %q", u.Slice, "faas-cp.slice")
	}
	if u.MemoryMax != "6G" {
		t.Errorf("MemoryMax = %q, want %q", u.MemoryMax, "6G")
	}
	if u.WantedBy != "multi-user.target" {
		t.Errorf("WantedBy = %q, want %q", u.WantedBy, "multi-user.target")
	}
	if u.Description == "" {
		t.Error("Description is empty")
	}

	// Slice units MUST render through RenderSlice() — NOT Render().
	// Render() emits [Service] which silently drops MemoryMax.
	// The 6 GB ceiling is the load-bearing directive for control-plane
	// admission (CLAUDE.md §11).
	body := string(u.RenderSlice())
	for _, want := range []string{
		"[Unit]",
		"Description=",
		"[Slice]",
		"MemoryMax=6G",
		"[Install]",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("RenderSlice() missing %q\n--- body ---\n%s", want, body)
		}
	}
	// And it MUST NOT contain a [Service] section — if it does,
	// systemd will read the unit as a service unit and ignore
	// MemoryMax (which lives in [Slice]).
	if strings.Contains(body, "[Service]") {
		t.Errorf("RenderSlice() contains [Service] section; MemoryMax will be ignored\n--- body ---\n%s", body)
	}

	// Note: Decode only walks [Unit] / [Service] / [Install] — it
	// can't round-trip a [Slice] body. That's a pkg/daemonunit
	// future improvement. The struct field assertions above are
	// the round-trip guarantee: a future change to UnitSlice()
	// must keep Slice/MemoryMax/WantedBy/Description populated, and
	// RenderSlice() must keep them written into the body.
	_ = daemonunit.Decode // keep the import; may be replaced by a slice-aware decoder later
}

func TestFaasCPSliceMemoryMaxMatchesControlPlaneReserve(t *testing.T) {
	if FaasCPSliceMemoryMax != "6G" {
		t.Errorf("FaasCPSliceMemoryMax = %q, want %q", FaasCPSliceMemoryMax, "6G")
	}
}
