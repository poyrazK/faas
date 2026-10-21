package managedpostgres

import (
	"errors"
	"fmt"
	"testing"
)

func TestLoggableReasonJoinsOnlyAdapterReasons(t *testing.T) {
	raw := errors.New("password=secret")
	inner := Reasonf(ErrUnavailable, "historical connection is not a read-only replay (recovery=%t read_only=%s)", false, "off")
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{name: "none", err: ErrUnavailable, want: ""},
		{name: "raw", err: fmt.Errorf("wrapped: %w", raw), want: ""},
		{name: "wrapped raw", err: Reasonf(raw, "historical point: read LSN"), want: "historical point: read LSN"},
		{name: "nested", err: Reasonf(inner, "historical point: read LSN"),
			want: "historical point: read LSN: historical connection is not a read-only replay (recovery=false read_only=off)"},
		{name: "normalized", err: withLoggableReason(ErrConflict, Reasonf(ErrConflict, "parent LSN 0/2 is not 0/1")), want: "parent LSN 0/2 is not 0/1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := LoggableReason(tc.err); got != tc.want {
				t.Fatalf("LoggableReason = %q, want %q", got, tc.want)
			}
		})
	}
	normalized := withLoggableReason(ErrConflict, Reasonf(ErrConflict, "parent LSN 0/2 is not 0/1"))
	if !errors.Is(normalized, ErrConflict) || normalized.Error() != ErrConflict.Error() {
		t.Fatalf("normalized error = %q, want the bare sentinel", normalized)
	}
	if bare := withLoggableReason(ErrUnavailable, raw); LoggableReason(bare) != "" || bare.Error() != ErrUnavailable.Error() {
		t.Fatal("an error without a reason must stay the bare sentinel")
	}
}
