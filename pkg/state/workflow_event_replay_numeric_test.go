package state

import "testing"

func TestWorkflowReplayJSONComparisonKeepsExactNumbers(t *testing.T) {
	for _, tc := range []struct {
		left, right string
		want        bool
	}{
		{`{"n":9007199254740992}`, `{"n":9007199254740993}`, false},
		{`{"n":1e400}`, `{"n":1e400}`, true},
		{`{"n":1000}`, `{"n":1e3}`, true},
		{`{"n":1} {}`, `{"n":1}`, false},
	} {
		if got := sameJSONDocument([]byte(tc.left), []byte(tc.right)); got != tc.want {
			t.Errorf("sameJSONDocument(%s, %s) = %t, want %t", tc.left, tc.right, got, tc.want)
		}
	}
}
