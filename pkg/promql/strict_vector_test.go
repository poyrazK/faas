package promql

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestQueryVectorStrictRejectsIncompleteEvidence(t *testing.T) {
	for _, body := range []string{
		`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"NaN"]}]}}`,
		`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,7]}]}}`,
		`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[null,"0"]}]}}`,
		`{"status":"success","data":{"resultType":"vector"}}`,
		`{"status":"error","data":{"resultType":"vector","result":[]}}`,
		`{"status":"success","warnings":["partial"],"data":{"resultType":"vector","result":[]}}`,
		`{"status":"success","infos":["partial"],"data":{"resultType":"vector","result":[]}}`,
	} {
		t.Run(body, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer srv.Close()
			if _, err := NewClient(srv.URL, srv.Client()).QueryVectorStrict(t.Context(), "up"); err == nil {
				t.Fatal("incomplete evidence accepted")
			}
		})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer srv.Close()
	if rows, err := NewClient(srv.URL, srv.Client()).QueryVectorStrict(t.Context(), "up"); err != nil || rows == nil || len(rows) != 0 {
		t.Fatalf("quiet evidence: %v %v", rows, err)
	}
}
