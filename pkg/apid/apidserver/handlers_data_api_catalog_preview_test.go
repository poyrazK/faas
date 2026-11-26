package apidserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDataAPICatalogPreviewRejectsArbitrarySQL(t *testing.T) {
	s := &server{managedPostgres: &managedpostgres.Service{}}
	request := httptest.NewRequest(http.MethodPost, "/v1/postgres/databases/db/data-api-catalog-preview", strings.NewReader(`{"sql":"SELECT secret FROM private_data"}`))
	response := httptest.NewRecorder()
	s.previewManagedPostgresDataAPICatalog(response, request, state.Account{})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("request body was accepted: %d", response.Code)
	}
	if strings.Contains(response.Body.String(), "private_data") {
		t.Fatal("request SQL was reflected in error")
	}
}
