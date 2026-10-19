package promql

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestQueryVectorStrictAtPinsTime(t *testing.T) {
	at := time.Date(2026, 10, 17, 7, 0, 0, 123456789, time.FixedZone("test", 3600))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("query") != `timestamp(up{job="apid"})` || r.URL.Query().Get("time") != at.UTC().Format(time.RFC3339Nano) {
			t.Errorf("request: %s", r.URL)
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"job":"apid"},"value":[1,"2"]}]}}`))
	}))
	defer srv.Close()
	rows, err := NewClient(srv.URL, srv.Client()).QueryVectorStrictAt(t.Context(), `timestamp(up{job="apid"})`, at)
	if err != nil || len(rows) != 1 || rows[0].Value != 2 {
		t.Fatal(rows, err)
	}
}
