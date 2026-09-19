package main

import (
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
		{name: "explicit true", explicit: true, requested: true, want: boolPtr(true)},
		{name: "explicit false", explicit: true, requested: false, want: boolPtr(false)},
		{name: "safe defaults to enabled", safe: true, want: boolPtr(true)},
		{name: "safe explicit true", safe: true, explicit: true, requested: true, want: boolPtr(true)},
		{name: "safe cannot disable", safe: true, explicit: true, wantErr: "requires first-wake 5xx rollback"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveDeployRollbackOn5xx(tt.safe, tt.explicit, tt.requested)
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
