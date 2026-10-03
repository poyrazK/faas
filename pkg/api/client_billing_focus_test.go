// adr: 380
package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFOCUSClientExportsBytesAndPropagatesProblems(t *testing.T) {
	for _, format := range []string{"", "zip", "csv", "metadata"} {
		t.Run(format, func(t *testing.T) {
			body := []byte{'P', 'K', 0, 255, '\n'}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/billing/focus" || r.URL.Query().Get("month") != "2026-09" || r.URL.Query().Get("format") != format || r.Header.Get("Authorization") != "Bearer test-token" {
					t.Errorf("incorrect request: %s %s", r.Method, r.URL)
				}
				_, _ = w.Write(body)
			}))
			defer srv.Close()
			got, err := NewClient(srv.URL, "test-token").ExportFOCUSInvoices(t.Context(), "2026-09", format)
			if err != nil || !bytes.Equal(got, body) {
				t.Fatalf("export bytes=%v error=%v", got, err)
			}
		})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		WriteProblem(w, NewProblem(http.StatusConflict, CodeConflict, "Invoice export unavailable", "invalid stored invoice"))
	}))
	defer srv.Close()
	_, err := NewClient(srv.URL, "token").ExportFOCUSInvoices(context.Background(), "2026-09", "zip")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Problem.Code != CodeConflict {
		t.Fatalf("lost RFC problem: %v", err)
	}
}
