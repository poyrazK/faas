package apidserver_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// TestExclusiveOperationMalformedIDIsNotFound: `gregale operations get
// notauuid` answered 503 "managed operation transition failed" on
// production-us rc.294 (Hunt 14 H14-65); an ID that is no UUID names no
// operation.
func TestExclusiveOperationMalformedIDIsNotFound(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{fmt.Errorf("operation by id: %w", &pgconn.PgError{Code: "22P02"}), http.StatusNotFound},
		{fmt.Errorf("operation by id: %w", &pgconn.PgError{Code: "57P01"}), http.StatusServiceUnavailable},
	} {
		rec := httptest.NewRecorder()
		apidserver.WriteExclusiveError(rec, tc.err)
		if rec.Code != tc.status {
			t.Errorf("WriteExclusiveError(%v) = %d, want %d: %s", tc.err, rec.Code, tc.status, rec.Body.String())
		}
	}
}
