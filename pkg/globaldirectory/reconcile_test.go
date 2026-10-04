package globaldirectory

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestGlobalReleaseReconcilerReceiptValidation(t *testing.T) {
	for _, tc := range []struct{ name, mode, outcome, diagnostic string }{
		{"redirect", "redirect", ReleasePending, DiagnosticRejected},
		{"unknown_field", "unknown", ReleasePending, DiagnosticReceipt},
		{"wrong_generation", "generation", ReleasePending, DiagnosticReceipt},
		{"oversized", "oversized", ReleasePending, DiagnosticReceipt},
		{"expired", "expired", ReleaseExpired, ""},
		{"bad_owner", "owner", ReleasePending, DiagnosticRejected},
		{"regional_rejection", "reject", ReleasePending, DiagnosticRejected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMemoryStore()
			owner := uuid.NewString()
			app, err := store.CreateApp(t.Context(), owner, "receipt-shop", 1)
			if err != nil {
				t.Fatal(err)
			}
			binding := Binding{CellID: uuid.NewString(), Region: "eu-fsn", CellGeneration: 1, RegionalAccountID: uuid.NewString(), RegionalAppID: uuid.NewString(), RegionalAppSlug: "regional-shop"}
			id := uuid.NewString()
			key, _, err := api.GenerateAPIKey()
			if err != nil {
				t.Fatal(err)
			}
			var redirects, puts atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+key {
					t.Error("wrong key")
				}
				if r.URL.Path == "/recipient" {
					redirects.Add(1)
					w.WriteHeader(http.StatusOK)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/binding") {
					accountID := binding.RegionalAccountID
					if tc.mode == "owner" {
						accountID = uuid.NewString()
					}
					_ = json.NewEncoder(w).Encode(api.CellAppBindingResponse{CellID: binding.CellID, Region: binding.Region, AccountID: accountID, AppID: binding.RegionalAppID, AppSlug: binding.RegionalAppSlug})
					return
				}
				if r.Method == http.MethodPut {
					puts.Add(1)
					http.Error(w, key, http.StatusConflict)
					return
				}
				switch tc.mode {
				case "redirect":
					http.Redirect(w, r, "/recipient", http.StatusTemporaryRedirect)
					return
				case "oversized":
					_, _ = w.Write([]byte(strings.Repeat("x", api.GlobalImageReleaseReceiptMaxBytes+1)))
					return
				case "unknown":
					_, _ = w.Write([]byte(`{"extra":true}`))
					return
				case "reject":
					w.WriteHeader(http.StatusNotFound)
					return
				}
				receipt := api.CellImageReleaseResponse{CellID: binding.CellID, Region: binding.Region, RegionalAccountID: binding.RegionalAccountID, RegionalAppID: binding.RegionalAppID, ReleaseID: id, Image: releaseTestImage, DeploymentStatus: "pending", CreatedAt: time.Now().UTC(), Ordering: &api.CellImageReleaseOrdering{GlobalAppID: app.ID, Generation: 1}}
				dep := uuid.NewString()
				receipt.DeploymentID = &dep
				if tc.mode == "generation" {
					receipt.Ordering.Generation = 2
				}
				if tc.mode == "expired" {
					receipt.DeploymentID = nil
					receipt.DeploymentStatus = "expired"
				}
				_ = json.NewEncoder(w).Encode(receipt)
			}))
			defer server.Close()
			if _, err := store.PutCell(t.Context(), Cell{ID: binding.CellID, Region: binding.Region, APIBase: server.URL, Enabled: true}); err != nil {
				t.Fatal(err)
			}
			if _, err := store.AttachTarget(t.Context(), owner, app.Slug, binding); err != nil {
				t.Fatal(err)
			}
			if _, err := store.PlanRelease(t.Context(), owner, app.Slug, id, releaseTestImage); err != nil {
				t.Fatal(err)
			}
			r, err := (Reconciler{Store: store, Verifier: Verifier{Client: server.Client()}}).Resume(t.Context(), owner, app.Slug, id, map[string]string{binding.Region: key})
			if err != nil || r.Targets[0].Outcome != tc.outcome || r.Targets[0].Diagnostic != tc.diagnostic {
				t.Fatalf("result: %+v %v", r, err)
			}
			if redirects.Load() != 0 {
				t.Fatal("followed redirect")
			}
			if puts.Load() != 0 && tc.mode != "reject" {
				t.Fatal("invalid receipt led to PUT")
			}
			encoded, err := json.Marshal(r)
			if err != nil || strings.Contains(string(encoded), key) {
				t.Fatal("remote secret stored")
			}
		})
	}
}

func TestGlobalReleaseReconcilerDelayedWorkerFences(t *testing.T) {
	for _, change := range []string{"new_release", "changed_origin"} {
		t.Run(change, func(t *testing.T) {
			store := NewMemoryStore()
			owner := uuid.NewString()
			app, err := store.CreateApp(t.Context(), owner, "delayed-shop", 1)
			if err != nil {
				t.Fatal(err)
			}
			binding := Binding{CellID: uuid.NewString(), Region: "eu-fsn", CellGeneration: 1, RegionalAccountID: uuid.NewString(), RegionalAppID: uuid.NewString(), RegionalAppSlug: "regional-shop"}
			entered := make(chan struct{})
			resume := make(chan struct{})
			var puts atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/binding") {
					_ = json.NewEncoder(w).Encode(api.CellAppBindingResponse{CellID: binding.CellID, Region: binding.Region, AccountID: binding.RegionalAccountID, AppID: binding.RegionalAppID, AppSlug: binding.RegionalAppSlug})
					return
				}
				if r.Method == http.MethodPut {
					puts.Add(1)
					w.WriteHeader(http.StatusAccepted)
					return
				}
				close(entered)
				<-resume
				w.WriteHeader(http.StatusNotFound)
			}))
			defer server.Close()
			if _, err := store.PutCell(t.Context(), Cell{ID: binding.CellID, Region: binding.Region, APIBase: server.URL, Enabled: true}); err != nil {
				t.Fatal(err)
			}
			if _, err := store.AttachTarget(t.Context(), owner, app.Slug, binding); err != nil {
				t.Fatal(err)
			}
			id := uuid.NewString()
			if _, err := store.PlanRelease(t.Context(), owner, app.Slug, id, releaseTestImage); err != nil {
				t.Fatal(err)
			}
			key, _, err := api.GenerateAPIKey()
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := (Reconciler{Store: store, Verifier: Verifier{Client: server.Client()}}).Resume(t.Context(), owner, app.Slug, id, map[string]string{binding.Region: key})
				done <- err
			}()
			<-entered
			if change == "new_release" {
				_, err = store.PlanRelease(t.Context(), owner, app.Slug, uuid.NewString(), releaseTestImage)
			} else {
				_, err = store.PutCell(t.Context(), Cell{ID: binding.CellID, Region: binding.Region, APIBase: "https://changed.example.com", Enabled: true})
			}
			close(resume)
			if err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			r, err := store.GetRelease(t.Context(), owner, app.Slug, id)
			if err != nil {
				t.Fatal(err)
			}
			expected := ReleaseSuperseded
			if change == "changed_origin" {
				expected = ReleaseBlocked
			}
			if r.Status() != expected || puts.Load() != 0 {
				t.Fatalf("late worker: %+v PUT=%d", r, puts.Load())
			}
		})
	}
}
