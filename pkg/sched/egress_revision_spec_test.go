package sched_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/sched"
)

// A wake spec names the revision of the egress policy it carries, so vmmd
// can prefer it over an older cached policy for the app.
func TestAppSpecCarriesEgressRevision(t *testing.T) {
	p := sched.AppSpec{EgressAllowlist: []string{"198.18.0.254/32"}, EgressPorts: []int{5432}, EgressRevision: 7}.ToProto()
	if p.GetEgressRevision() != 7 || len(p.GetEgressAllowlist()) != 1 || len(p.GetEgressPorts()) != 1 {
		t.Fatalf("proto egress = %v ports %v revision %d, want the spec's policy at revision 7", p.GetEgressAllowlist(), p.GetEgressPorts(), p.GetEgressRevision())
	}
}
