package api_test

// adr: 785
import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

func TestQuotaTransportSDKPreservesCanonicalWireAndRefusesRedirect(t *testing.T) {
	cell, app, allocation := uuid.NewString(), uuid.NewString(), uuid.NewString()
	binding := api.RegionalRuntimeQuotaBinding{CellID: cell, Region: "eu-fsn", CellGeneration: 3, RegionalAccountID: uuid.NewString(), RegionalAppID: app, RegionalAppSlug: "regional-shop", APIBase: "https://api.example.com"}
	request := api.RegionalRuntimeQuotaDelivery{Binding: binding, Authority: api.RegionalRuntimeQuotaEnvelope{KeyID: "test-key", Payload: "test-payload", Signature: "test-signature"}}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var original runtimequota.Delivery
	if err := json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	canonical, err := json.Marshal(original)
	if err != nil || string(canonical) != string(raw) {
		t.Fatal("SDK changed original binding spelling", string(raw), string(canonical), err)
	}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Redirect(w, r, "/recipient", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	c := api.NewClient(srv.URL, "test-account-key").SetCompletionCache(nil)
	for _, kind := range []string{"grant", "retirement", "directory"} {
		t.Run(kind, func(t *testing.T) {
			invoke := func(id string) error {
				var err error
				switch kind {
				case "grant":
					_, err = c.AcceptCellRuntimeQuota(t.Context(), cell, app, id, request)
				case "retirement":
					_, err = c.RetireCellRuntimeQuota(t.Context(), cell, app, id, request)
				case "directory":
					_, err = c.DeliverGlobalRuntimeQuota(t.Context(), "global-shop", id, "eu-fsn", api.DeliverGlobalRuntimeQuotaRequest{RegionalAPIKey: "request-only-key"})
				}
				return err
			}
			before := calls
			if err := invoke(allocation); err == nil || calls != before+1 {
				t.Fatal("redirect followed", calls, err)
			}
			before = calls
			for _, invalid := range []string{"bad", uuid.Nil.String()} {
				if err := invoke(invalid); err == nil || calls != before {
					t.Fatal("invalid identity reached network", calls, err)
				}
			}
		})
	}
}
