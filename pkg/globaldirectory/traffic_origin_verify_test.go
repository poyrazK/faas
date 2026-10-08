package globaldirectory

// adr: 785
import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestRegionalTrafficOriginVerification(t *testing.T) {
	token, _, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, domain, proofOrigin string
		success                   bool
	}{
		{"legacy", "", "", true},
		{"matching", "apps.example.com", "https://regional-shop.apps.example.com", true},
		{"missing_capability", "apps.example.com", "", false},
		{"wrong_domain", "apps.example.com", "https://regional-shop.other.example.com", false},
		{"wrong_slug", "apps.example.com", "https://other-shop.apps.example.com", false},
		{"management_origin", "apps.example.com", "https://api.example.com", false},
		{"trailing_slash", "apps.example.com", "https://regional-shop.apps.example.com/", false},
		{"port", "apps.example.com", "https://regional-shop.apps.example.com:443", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cell := Cell{ID: uuid.NewString(), Region: "eu-fsn", AppsDomain: tc.domain, Enabled: true, Generation: 4}
			proof := api.CellAppBindingResponse{CellID: cell.ID, Region: cell.Region, AccountID: uuid.NewString(), AppID: uuid.NewString(), AppSlug: "regional-shop", AppOrigin: tc.proofOrigin}
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				wantQuery := ""
				if tc.domain != "" {
					wantQuery = "traffic_origin=1"
				}
				if r.URL.RawQuery != wantQuery || r.Header.Get("Authorization") != "Bearer "+token {
					t.Error("wrong negotiated proof request")
				}
				_ = json.NewEncoder(w).Encode(proof)
			}))
			defer srv.Close()
			cell.APIBase = srv.URL
			binding, err := (Verifier{Client: srv.Client()}).Verify(t.Context(), cell, proof.AppSlug, token)
			if tc.success {
				if err != nil || binding.RegionalAppID != proof.AppID || binding.CellGeneration != cell.Generation {
					t.Fatal(binding, err)
				}
			} else if !errors.Is(err, ErrVerification) || binding != (Binding{}) {
				t.Fatal(binding, err)
			}
		})
	}
}
