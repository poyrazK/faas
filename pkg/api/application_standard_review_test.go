package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestApplicationStandardReviewRequestStrictJSON(t *testing.T) {
	id := uuid.NewString()
	valid := `{"scope":"organization","scope_id":"` + id + `","standard_id":"` + id + `","admission_version":1,"expected_revision":0,"active":true,"batch_size":1}`
	var req ApplicationStandardReviewRequest
	if err := json.Unmarshal([]byte(valid), &req); err != nil || req.ExpectedRevision != 0 {
		t.Fatalf("valid preview: %+v %v", req, err)
	}
	for _, raw := range []string{
		strings.Replace(valid, `"active":true,`, "", 1), strings.Replace(valid, `"expected_revision":0,`, "", 1), strings.Replace(valid, `"batch_size":1`, `"batch_size":null`, 1), strings.Replace(valid, `"active":true`, `"active":null`, 1), strings.Replace(valid, `"active":true`, `"active":true,"active":false`, 1), strings.Replace(valid, `"batch_size":1`, `"batch_size":101`, 1), strings.Replace(valid, `"batch_size":1`, `"batch_size":1,"private":true`, 1), valid + ` {}`, strings.Replace(valid, `"active":true`, `"active":false`, 1), strings.Replace(valid, `"scope":"organization"`, `"scope":"unknown"`, 1), strings.Replace(valid, id, uuid.Nil.String(), 1),
	} {
		if err := json.Unmarshal([]byte(raw), &req); err == nil {
			t.Fatalf("accepted invalid request %s", raw)
		}
	}
	update := strings.Replace(valid, `"expected_revision":0,"active":true`, `"assignment_id":"`+id+`","expected_revision":1,"active":false`, 1)
	if err := json.Unmarshal([]byte(update), &req); err != nil || req.Active {
		t.Fatalf("explicit false update lost: %+v %v", req, err)
	}
}
