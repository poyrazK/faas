package runtimequota

// adr: 633
import (
	"strings"
	"testing"
)

func TestLaunchSourceActualLookupSpelling(t *testing.T) {
	g, _, _, _ := fixture(t)
	p := capacityPermitFixture(g)
	p.InstanceID = "abcdef01-2345-4678-9abc-def012345678"
	for _, key := range []string{p.InstanceID, strings.ReplaceAll(p.InstanceID, "-", "")} {
		if err := (LaunchSource{Permit: p, InstanceKey: key}).Validate(); err != nil {
			t.Fatal("native UUID lookup refused", err)
		}
	}
	for _, key := range []string{"", strings.ToUpper(p.InstanceID), "{" + p.InstanceID + "}", "urn:uuid:" + p.InstanceID, p.RunID} {
		if err := (LaunchSource{Permit: p, InstanceKey: key}).Validate(); err == nil {
			t.Fatal("ambiguous/different native key accepted", key)
		}
	}
}
