package automationchecks

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestCanonicalJSONPreservesTypesAndNormalizesNumbers(t *testing.T) {
	for _, pair := range [][2]string{
		{`{"n":1}`, `{"n":1.00}`},
		{`[0,-0.0,9007199254740993]`, `[0,0,9007199254740993.0]`},
		{`{"n":1e100000}`, `{"n":10e99999}`},
	} {
		a, errA := CanonicalJSON(json.RawMessage(pair[0]))
		b, errB := CanonicalJSON(json.RawMessage(pair[1]))
		if errA != nil || errB != nil || !bytes.Equal(a, b) {
			t.Fatalf("canonical mismatch %s %s: %s %s %v %v", pair[0], pair[1], a, b, errA, errB)
		}
	}
	for _, pair := range [][2]string{{`1`, `"1e0"`}, {`1`, `{"Digits":"1","Exponent":"0","Negative":false}`}, {`9007199254740992`, `9007199254740993`}} {
		a, _ := CanonicalJSON(json.RawMessage(pair[0]))
		b, _ := CanonicalJSON(json.RawMessage(pair[1]))
		if bytes.Equal(a, b) {
			t.Fatalf("distinct values collided: %s %s", pair[0], pair[1])
		}
	}
	if _, err := CanonicalJSON(json.RawMessage(`1 2`)); err == nil {
		t.Fatal("accepted trailing JSON")
	}
}
