package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientGetOverageCap(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  int64
	}{
		{value: "null"},
		{value: "0"},
		{value: "1250", want: 1250},
		{value: "9007199254740993", want: 9007199254740993},
	} {
		t.Run(tc.value, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/account/overage-cap" {
					t.Errorf("request = %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"overage_cap_cents":%s}`, tc.value)
			}))
			defer srv.Close()
			got, err := NewClient(srv.URL, "test-key").GetOverageCap(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if tc.value == "null" {
				if got.OverageCapCents != nil {
					t.Fatalf("cap = %d, want nil", *got.OverageCapCents)
				}
			} else if got.OverageCapCents == nil || *got.OverageCapCents != tc.want {
				t.Fatalf("cap = %v, want %d", got.OverageCapCents, tc.want)
			}
		})
	}
}

func TestClientGetOverageCap_ReadFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		WriteProblem(w, ErrInternal("could not read overage cap"))
	}))
	defer srv.Close()
	if _, err := NewClient(srv.URL, "test-key").GetOverageCap(context.Background()); err == nil {
		t.Fatal("storage failure returned success")
	}
}
