// adr: 950
package api

import "testing"

// Wake-ahead fails closed to off, so an older gateway never admits
// speculative instances for a value it does not understand.
func TestServiceWakeAheadEffectiveAndNormalize(t *testing.T) {
	for _, test := range []struct {
		in, want ServiceWakeAhead
	}{
		{in: "", want: ServiceWakeAheadOff},
		{in: ServiceWakeAheadOff, want: ServiceWakeAheadOff},
		{in: ServiceWakeAheadDeclared, want: ServiceWakeAheadDeclared},
		{in: ServiceWakeAhead("always"), want: ServiceWakeAheadOff},
	} {
		if got := test.in.Effective(); got != test.want {
			t.Errorf("%q Effective() = %q, want %q", test.in, got, test.want)
		}
	}
	if got, err := NormalizeServiceWakeAhead(" Declared "); err != nil || got != ServiceWakeAheadDeclared {
		t.Fatalf("normalized wake-ahead = %q, %v", got, err)
	}
	if got, err := NormalizeServiceWakeAhead(""); err != nil || got != "" {
		t.Fatalf("omitted wake-ahead = %q, %v; want empty", got, err)
	}
	if _, err := NormalizeServiceWakeAhead("always"); err == nil {
		t.Fatal("accepted unknown wake-ahead mode")
	}
}
