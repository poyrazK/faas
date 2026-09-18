package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPutJobRegistryCredentialRequestValidateMirrorsAppRequest(t *testing.T) {
	valid := PutJobRegistryCredentialRequest{Registry: "ghcr.io", Username: "robot", Password: "secret"}
	if got := valid.Validate(); got != nil {
		t.Fatalf("Validate(valid) = %+v, want nil", got)
	}
	invalid := PutJobRegistryCredentialRequest{Registry: "ghcr.io", Username: "robot"}
	if got := invalid.Validate(); got == nil || got.Code != CodeInvalidRegistryHost {
		t.Fatalf("Validate(invalid) = %+v, want %s", got, CodeInvalidRegistryHost)
	}
}

func TestJobRegistryCredentialResponsesNeverContainPassword(t *testing.T) {
	resp := JobRegistryCredentialListResponse{
		Credentials: []JobRegistryCredentialResponse{{
			Registry: "ghcr.io", Username: "robot", CreatedAt: "2026-09-18T00:00:00Z", UpdatedAt: "2026-09-18T00:00:00Z",
		}},
		QuotaMax: 2,
		Count:    1,
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(strings.ToLower(string(b)), "password") {
		t.Fatalf("response contains password: %s", b)
	}
}
