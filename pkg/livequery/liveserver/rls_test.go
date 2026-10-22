package liveserver

import (
	"encoding/json"
	"testing"
)

func TestRLSClaimsMatchTheDataAPIGateway(t *testing.T) {
	canonical, ok := rlsClaims(json.RawMessage(`{"sub":"alice","exp":1,"iat":2,"nbf":3,"jti":"j","tenant":"t1",
		"gregale_access":"server","gregale_server_key":"a","gregale_sql_deadline":{"version":1},"gregale_refresh_hash":"h","gregale_serving_fingerprint":"f"}`))
	if !ok {
		t.Fatal("valid claims refused")
	}
	if want := `{"gregale_access":"user","sub":"alice","tenant":"t1"}`; canonical != want {
		t.Fatalf("claims = %s, want %s", canonical, want)
	}
	refreshed, _ := rlsClaims(json.RawMessage(`{"tenant":"t1","sub":"alice","exp":99,"gregale_access":"anonymous"}`))
	if refreshed != canonical {
		t.Fatalf("a refreshed token must keep its plan group: %s != %s", refreshed, canonical)
	}
	for _, raw := range []string{`null`, `[]`, `"x"`, `{`} {
		if _, ok := rlsClaims(json.RawMessage(raw)); ok {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestSessionClaimsTrustOnlyTheGatewayShapeForKeylessReads(t *testing.T) {
	for _, tc := range []struct {
		access, raw, want string
	}{
		{"anonymous", `{"gregale_access":"anonymous"}`, `{"gregale_access":"anonymous"}`},
		{"server", `{"gregale_access":"server","gregale_server_key":"b"}`, `{"gregale_access":"server","gregale_server_key":"b"}`},
		{"server", `{"sub":"user-7","gregale_server_key":"a","gregale_access":"server"}`, `{"gregale_access":"server","gregale_server_key":"a","sub":"user-7"}`},
		{"", `{"sub":"alice","gregale_access":"server"}`, `{"gregale_access":"user","sub":"alice"}`},
	} {
		got, ok := sessionClaims(tc.access, json.RawMessage(tc.raw))
		if !ok || got != tc.want {
			t.Fatalf("%s %s: %q %v, want %q", tc.access, tc.raw, got, ok, tc.want)
		}
	}
	for _, tc := range []struct{ access, raw string }{
		{"anonymous", `{"gregale_access":"anonymous","sub":"alice"}`},
		{"anonymous", `{"gregale_access":"user"}`},
		{"server", `{"gregale_access":"server"}`},
		{"server", `{"gregale_access":"server","gregale_server_key":"c"}`},
		{"server", `{"gregale_access":"server","gregale_server_key":"a","tenant":"t1"}`},
		{"server", `{"gregale_access":"server","gregale_server_key":"a","sub":""}`},
		{"anonymous", `{"gregale_access":"anonymous","n":1}`},
		{"admin", `{"gregale_access":"admin"}`},
	} {
		if got, ok := sessionClaims(tc.access, json.RawMessage(tc.raw)); ok {
			t.Fatalf("%s %s accepted as %q", tc.access, tc.raw, got)
		}
	}
}
