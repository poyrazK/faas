// adr: 568 — guest HTTP, TCP and DNS share the original qualification guard.
package main

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/gateway"
)

type qualificationCallerGuardStub struct {
	node     string
	held     bool
	err      error
	observed string
}

func (s *qualificationCallerGuardStub) EnvironmentQualificationNetworkNode(_ context.Context, _ string, ip string) (string, error) {
	s.observed = ip
	return s.node, s.err
}
func (s *qualificationCallerGuardStub) EnvironmentQualificationNetworkCaller(context.Context, string, string) (bool, error) {
	return s.held, s.err
}

func TestOrdinaryQualificationCallerGuardRejectsPrivateSlotsAndLookupFailures(t *testing.T) {
	for _, test := range []struct {
		name, node, remote string
		held               bool
		err                error
		deny               bool
	}{
		{"ordinary", uuid.NewString(), "10.100.0.3:12345", false, nil, false},
		{"held", uuid.NewString(), "10.100.0.3:12345", true, nil, true},
		{"unknown", "", "10.100.0.3:12345", false, nil, false},
		{"unavailable", "", "10.100.0.3:12345", false, errors.New("database unavailable"), true},
		{"unparseable", "", "forged", false, nil, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := &qualificationCallerGuardStub{node: test.node, held: test.held, err: test.err}
			err := ordinaryQualificationCallerGuard(t.Context(), s, "original-node", test.remote)
			if (err != nil) != test.deny {
				t.Fatal(err)
			}
			if test.held && !errors.Is(err, gateway.ErrServiceProxyDenied) {
				t.Fatal("held caller retained ordinary authority", err)
			}
		})
	}
}
