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
