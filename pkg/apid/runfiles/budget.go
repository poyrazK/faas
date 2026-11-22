package runfiles

import (
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reqbudget"
)

// BudgetedHandler gives binary transfers their central, bounded transfer
// budget. Metadata and admission requests retain the ordinary API budget.
// Both paths preserve caller cancellation and any earlier parent deadline.
func BudgetedHandler(next http.Handler, ordinary reqbudget.MiddlewareConfig) (http.Handler, error) {
	transfer := ordinary
	transfer.Default = api.ExecutionFileUploadTimeout
	transfer.Max = api.ExecutionFileUploadTimeout
	transfer.DefaultFor = nil
	transfer, err := reqbudget.NewMiddlewareConfig(transfer)
	if err != nil {
		return nil, err
	}
	normalHandler := ordinary.Middleware(next)
	transferHandler := transfer.Middleware(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isTransferRequest(r) {
			transferHandler.ServeHTTP(w, r)
			return
		}
		normalHandler.ServeHTTP(w, r)
	}), nil
}

func isTransferRequest(r *http.Request) bool {
	if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/apps/") && strings.Contains(r.URL.Path, "/run-artifact-deliveries/") {
		return len(strings.Split(strings.Trim(r.URL.Path, "/"), "/")) == 5
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		return false
	}
	id, ok := strings.CutPrefix(r.URL.Path, "/v1/execution-files/")
	if !ok {
		return false
	}
	id, ok = strings.CutSuffix(id, "/content")
	return ok && id != "" && !strings.Contains(id, "/")
}
