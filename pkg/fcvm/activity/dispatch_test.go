// adr: 1359
package activity

import "testing"

func TestOnlyDispatchRequiresExactSoleOriginalForward(t *testing.T) {
	a := New(nil)
	if a.OnlyDispatch("vm", "job", 1) {
		t.Fatal("unobserved forward accepted")
	}
	a.BeginDispatch("vm", "job", 1)
	if !a.OnlyDispatch("vm", "job", 1) || a.OnlyDispatch("vm", "other", 1) || a.OnlyDispatch("vm", "job", 2) {
		t.Fatal("dispatch identity differs")
	}
	a.Begin("vm")
	if a.OnlyDispatch("vm", "job", 1) {
		t.Fatal("unidentified concurrent request ignored")
	}
	a.End("vm")
	a.BeginDispatch("vm", "job", 1)
	if a.OnlyDispatch("vm", "job", 1) {
		t.Fatal("duplicate dispatch ignored")
	}
	a.EndDispatch("vm", "job", 1)
	if !a.OnlyDispatch("vm", "job", 1) {
		t.Fatal("remaining original forward disappeared")
	}
	a.EndDispatch("vm", "job", 1)
	if a.OnlyDispatch("vm", "job", 1) {
		t.Fatal("completed forward remained eligible")
	}
	a.BeginDispatch("vm", "job", 1)
	a.Forget("vm")
	if a.OnlyDispatch("vm", "job", 1) {
		t.Fatal("forgotten owner was reconstructed")
	}
}
