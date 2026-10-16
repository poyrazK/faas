package promql

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRecordingRulesWireAndLabelPresence(t *testing.T) {
	for _, body := range []string{
		`{"type":"recording","name":"stage:a","query":"up"}`,
		`{"type":"recording","name":"stage:a","query":"up","labels":{}}`,
		`{"type":"recording","name":"stage:a","query":"up","labels":null}`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.String() != "/api/v1/rules?type=record" {
				t.Errorf("wrong recording read: %s", r.URL)
			}
			_, _ = w.Write([]byte(`{"status":"success","data":{"groups":[{"name":"g","file":"rules.yml","rules":[` + body + `]}]}}`))
		}))
		groups, err := NewClient(srv.URL, srv.Client()).RecordingRules(t.Context())
		srv.Close()
		if err != nil || len(groups) != 1 || len(groups[0].Rules) != 1 {
			t.Fatalf("recording wire: %v", err)
		}
		r := groups[0].Rules[0]
		if r.LabelsNull != (body[len(body)-5:] == "null}") || r.DurationReported || r.KeepFiringFor != nil {
			t.Fatalf("field presence: %+v", r)
		}
		if err := json.Unmarshal([]byte(`{"labels":{"severity":"warn"}}`), &r); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(`{}`), &r); err != nil || r.LabelsNull || r.Labels != nil {
			t.Fatal("prior labels leaked into next decode")
		}
	}
}
