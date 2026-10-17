// ADR-1549: help messages remain bounded plain text and context follows current permissions.
package productcontrol

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestProductHelpValidation(t *testing.T) {
	for _, text := range []string{"", " \n\t", "bad\x00value", strings.Repeat("é", api.ProductControlHelpTextMaxBytes/2+1), string([]byte{0xff})} {
		if HelpText(text) {
			t.Fatalf("invalid help message accepted: %q", text)
		}
	}
	if !HelpText("Please check\nthis <script>literal text</script>.") {
		t.Fatal("valid plain text rejected")
	}
	r := api.ProductHelpCreate{ID: uuid.NewString(), Kind: "workspace", Message: "Question"}
	if err := ValidateHelpCreate(r); err != nil {
		t.Fatal(err)
	}
	r.OperationID = uuid.NewString()
	if ValidateHelpCreate(r) == nil {
		t.Fatal("workspace body accepted a run reference")
	}
	r.Kind = "run"
	if err := ValidateHelpCreate(r); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"open", "in_progress", "resolved"} {
		if ValidateHelpUpdate(api.ProductHelpUpdate{ExpectedVersion: 1, Status: status, Response: "Reviewed"}) != nil {
			t.Fatal("valid status rejected", status)
		}
	}
	if ValidateHelpUpdate(api.ProductHelpUpdate{ExpectedVersion: 0, Status: "resolved", Response: "Done"}) == nil || HelpVisible("feature", api.ProductPermissions{ViewWork: true}) || HelpVisible("run", api.ProductPermissions{ViewBilling: true}) || HelpVisible("workspace", api.ProductPermissions{}) {
		t.Fatal("help authority/version validation failed")
	}
}
