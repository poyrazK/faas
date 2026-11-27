package runfiles_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/runfiles"
	"github.com/onebox-faas/faas/pkg/reqbudget"
)

func TestStoredFileTransfersOutliveOrdinaryRequestBudget(t *testing.T) {
	const path = "/v1/execution-files/5f3ac0e9-5335-4874-8450-92d715aa16c7/content"
	for _, tc := range []struct {
		method, path string
		transfer     bool
	}{
		{http.MethodPut, path, true},
		{http.MethodGet, path, true},
		{http.MethodPost, path, false},
		{http.MethodGet, path + "/extra", false},
		{http.MethodGet, "/v1/execution-files//content", false},
		{http.MethodGet, "/v1/execution-files/a/b/content", false},
		{http.MethodGet, "/v1/execution-files/file-id", false},
		{http.MethodPost, "/v2/executions", false},
		{http.MethodPost, "/v1/apps/reports/run-artifact-deliveries/reports", true},
		{http.MethodGet, "/v1/apps/reports/run-artifact-deliveries/reports/id", false},
		{http.MethodPost, "/v1/apps/reports/run-artifact-deliveries/reports/id/download-url", false},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			handler, err := runfiles.BudgetedHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !tc.transfer {
					select {
					case <-r.Context().Done():
					case <-time.After(time.Second):
						t.Fatal("ordinary request lost its short deadline")
					}
					return
				}
				if deadline, ok := r.Context().Deadline(); !ok || time.Until(deadline) < time.Minute {
					t.Fatal("transfer inherited the ordinary request deadline")
				}
				select {
				case <-r.Context().Done():
					t.Fatal("transfer ended at the ordinary request budget")
				case <-time.After(30 * time.Millisecond):
				}
				w.WriteHeader(http.StatusNoContent)
			}), reqbudget.MiddlewareConfig{Default: 10 * time.Millisecond, Max: 10 * time.Millisecond, Route: "admin"})
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, nil))
			want := http.StatusGatewayTimeout
			if tc.transfer {
				want = http.StatusNoContent
			}
			if response.Code != want {
				t.Fatalf("status = %d, want %d", response.Code, want)
			}
		})
	}
}

func TestStoredFileTransferBudgetKeepsCallerLifetime(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancellation", true: "earlier_deadline"}[deadline], func(t *testing.T) {
			parent, cancel := context.WithCancel(t.Context())
			defer cancel()
			wantErr := context.Canceled
			if deadline {
				var stop context.CancelFunc
				parent, stop = context.WithTimeout(parent, 20*time.Millisecond)
				defer stop()
				wantErr = context.DeadlineExceeded
			}
			handler, err := runfiles.BudgetedHandler(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				if deadline {
					want, _ := parent.Deadline()
					got, ok := r.Context().Deadline()
					if !ok || !got.Equal(want) {
						t.Fatal("transfer extended its caller's earlier deadline")
					}
				} else {
					cancel()
				}
				select {
				case <-r.Context().Done():
					if !errors.Is(r.Context().Err(), wantErr) {
						t.Fatal("transfer lost its caller cancellation cause")
					}
				case <-time.After(time.Second):
					t.Fatal("transfer ignored its caller lifetime")
				}
			}), reqbudget.MiddlewareConfig{Default: time.Second, Max: time.Second, Route: "admin"})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPut, "/v1/execution-files/file-id/content", nil).WithContext(parent)
			handler.ServeHTTP(httptest.NewRecorder(), request)
		})
	}
}
