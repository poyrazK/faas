package main_test

import (
	"github.com/onebox-faas/faas/cmd/gregale"
	"strings"
	"testing"
)

func TestResolveDeployRollbackOn5xx(t *testing.T) {
	tests := []struct {
		name      string
		safe      bool
		explicit  bool
		requested bool
		want      *bool
		wantErr   string
	}{
		{name: "default deploy leaves server default", want: nil},
		{name: "explicit true", explicit: true, requested: true, want: main.BoolPtr(true)},
		{name: "explicit false", explicit: true, requested: false, want: main.BoolPtr(false)},
		{name: "safe defaults to enabled", safe: true, want: main.BoolPtr(true)},
		{name: "safe explicit true", safe: true, explicit: true, requested: true, want: main.BoolPtr(true)},
		{name: "safe cannot disable", safe: true, explicit: true, wantErr: "requires first-wake 5xx rollback"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := main.ResolveDeployRollbackOn5xx(tt.safe, tt.explicit, tt.requested)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tt.want == nil {
				if got != nil {
					t.Fatalf("rollback policy = %v, want nil", *got)
				}
				return
			}
			if got == nil || *got != *tt.want {
				t.Fatalf("rollback policy = %v, want %v", got, *tt.want)
			}
		})
	}
}
