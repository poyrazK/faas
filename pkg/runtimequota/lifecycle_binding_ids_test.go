package runtimequota

// adr: 627
import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestLifecycleBindingCompactRowIDsPreserveCanonicalClaims(t *testing.T) {
	g, _, _, _ := fixture(t)
	p := Placement{Permit: capacityPermitFixture(g)}
	p.Permit.Kind = CapacityWarm
	b := LifecycleBinding{InstanceID: p.Permit.InstanceID, AppID: p.Permit.Grant.Binding.RegionalAppID, AccountID: p.Permit.Grant.Binding.RegionalAccountID, DeploymentID: p.Permit.DeploymentID, Plan: p.Permit.Grant.Policy.Plan, Purpose: CapacityWarm}
	for _, id := range []*string{&b.InstanceID, &b.AppID, &b.AccountID, &b.DeploymentID} {
		*id = strings.ReplaceAll(*id, "-", "")
	}
	original := p.Clone()
	if err := p.ValidateBinding(b); err != nil || p.Permit != original.Permit {
		t.Fatal("compact row key changed/rejected canonical claims", err)
	}
	b.InstanceID = uuid.NewString()
	if err := p.ValidateBinding(b); err == nil {
		t.Fatal("different UUID matched the physical permit")
	}
	for _, actual := range []string{"", "opaque-runtime", uuid.Nil.String(), uuid.NewString()} {
		if MatchesID(p.Permit.InstanceID, actual) {
			t.Fatal("unbound runtime key matched", actual)
		}
	}
	compact := strings.ReplaceAll(p.Permit.InstanceID, "-", "")
	if MatchesID(compact, p.Permit.InstanceID) {
		t.Fatal("noncanonical protocol claims were accepted")
	}
}
