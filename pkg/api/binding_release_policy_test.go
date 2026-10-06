package api

import "testing"

func TestBindingReleasePolicyValidation(t *testing.T) {
	zero := int64(0)
	for _, r := range []SetBindingReleasePolicyRequest{
		{Mode: "enforce"}, {Mode: "future", ExpectedRevision: &zero}, {Mode: "off", ExpectedRevision: &zero},
		{Mode: "enforce", ExpectedRevision: &zero, MaxVerificationAge: "0s"},
		{Mode: "enforce", ExpectedRevision: &zero, MaxVerificationAge: "25h"},
		{Mode: "enforce", ExpectedRevision: &zero, MaxVerificationAge: "1.5s"},
		{Mode: "off", ExpectedRevision: &zero, Reason: "secret\nvalue"},
	} {
		if ValidateBindingReleasePolicyRequest(r) == nil {
			t.Fatalf("accepted %+v", r)
		}
	}
	for _, r := range []SetBindingReleasePolicyRequest{{Mode: "enforce", ExpectedRevision: &zero}, {Mode: "off", ExpectedRevision: &zero, Reason: "incident recovery"}, {Mode: "enforce", ExpectedRevision: &zero, MaxVerificationAge: "24h"}} {
		if err := ValidateBindingReleasePolicyRequest(r); err != nil {
			t.Fatal(err)
		}
	}
}
