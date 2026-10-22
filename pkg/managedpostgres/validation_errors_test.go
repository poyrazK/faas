package managedpostgres

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestCreateRejectionsNameTheFieldAndAlternatives pins the production-us
// hunt #9 finding that every create rejection carried a bare sentinel, so the
// API detail read "invalid managed postgres request" or "managed postgres
// feature unsupported" with no field, value or alternative. The sentinels
// must still match with errors.Is.
func TestCreateRejectionsNameTheFieldAndAlternatives(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*CreateRequest)
		is     error
		want   string
	}{
		{name: "name", mutate: func(r *CreateRequest) { r.Name = "Orders_DB" }, is: ErrInvalid,
			want: "name must start with a lowercase letter"},
		{name: "region syntax", mutate: func(r *CreateRequest) { r.Spec.Region = "US East" }, is: ErrInvalid,
			want: "region must be lowercase letters"},
		{name: "major range", mutate: func(r *CreateRequest) { r.Spec.PostgresMajor = 11 }, is: ErrInvalid,
			want: "postgres major 11 is outside 12-99"},
		{name: "class", mutate: func(r *CreateRequest) { r.Spec.Class = "huge" }, is: ErrInvalid,
			want: "service class must be development, burstable or production"},
		{name: "region without backend", mutate: func(r *CreateRequest) { r.Spec.Region = "mars-1" }, is: ErrUnsupported,
			want: `no database backend in region "mars-1" (available: us-east-1)`},
		{name: "unoffered major", mutate: func(r *CreateRequest) { r.Spec.PostgresMajor = 14 }, is: ErrUnsupported,
			want: "postgres major 14 is not offered here (offered: 16, 17)"},
		{name: "unoffered class", mutate: func(r *CreateRequest) { r.Spec.Class = ClassProduction }, is: ErrUnsupported,
			want: "service class production is not offered here (offered: development, burstable)"},
		{name: "restore window", mutate: func(r *CreateRequest) { r.Spec.RestoreWindowSeconds = 30 * 24 * 60 * 60 }, is: ErrUnsupported,
			want: "restore window 2592000 s exceeds the 604800 s maximum"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := &fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady}
			service := testService(t, testRegistry(t, provider, nil), NewMemoryStore())
			request := CreateRequest{AccountID: "account-a", Name: "orders", Spec: testSpec()}
			tc.mutate(&request)
			_, err := service.Create(context.Background(), request)
			if !errors.Is(err, tc.is) {
				t.Fatalf("Create = %v, want %v", err, tc.is)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Create = %q, want it to contain %q", err.Error(), tc.want)
			}
			if provider.provisionCalls != 0 {
				t.Fatalf("provider contacted %d times", provider.provisionCalls)
			}
		})
	}
}
