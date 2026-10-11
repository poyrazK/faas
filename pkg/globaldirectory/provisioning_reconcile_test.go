package globaldirectory

// adr: 797
import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestGlobalAppProvisioningRefusesInvalidProofAndRedirect(t *testing.T) {
	for _, mode := range []string{"foreign_account", "unknown_field", "redirect", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			cell := Cell{ID: uuid.NewString(), Region: "eu-fsn", Generation: 1, Enabled: true}
			account := uuid.NewString()
			key, _, _ := api.GenerateAPIKey()
			var forwarded atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/recipient" {
					forwarded.Add(1)
					return
				}
				p := api.CellAccountBindingResponse{CellID: cell.ID, Region: cell.Region, AccountID: account}
				switch mode {
				case "foreign_account":
					p.AccountID = uuid.NewString()
				case "unknown_field":
					_, _ = w.Write([]byte(`{"unexpected":true}`))
					return
				case "redirect":
					http.Redirect(w, r, "/recipient", http.StatusTemporaryRedirect)
					return
				case "oversized":
					_, _ = w.Write([]byte(strings.Repeat("x", api.GlobalAppProvisionRequestMaxBytes+1)))
					return
				}
				_ = json.NewEncoder(w).Encode(p)
			}))
			defer server.Close()
			cell.APIBase = server.URL
			if err := (Verifier{Client: server.Client()}).VerifyAccount(t.Context(), cell, key, account); err == nil {
				t.Fatal("invalid account proof accepted")
			}
			if forwarded.Load() != 0 {
				t.Fatal("regional credential followed redirect")
			}
		})
	}
}
