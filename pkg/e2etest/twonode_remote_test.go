package e2etest

import (
	"strings"
	"testing"
)

func TestTwoNodeSkipReason_LocalModeDeclines(t *testing.T) {
	reason := TwoNodeSkipReason(false)
	if reason == "" {
		t.Fatal("local mode returned no skip reason; the drill would wait out its " +
			"full 90s budget for a lifecycle flip no process on the host can perform")
	}
	// The reason has to name the way out, or the next person reads it as a
	// product failure — which is how these three read for several gate runs.
	for _, want := range []string{"FAAS_TWO_NODE_REMOTE", "run-native-m9-acceptance.sh", "schedd"} {
		if !strings.Contains(reason, want) {
			t.Errorf("skip reason does not mention %q: %s", want, reason)
		}
	}
}

func TestTwoNodeSkipReason_RemoteModeRuns(t *testing.T) {
	if reason := TwoNodeSkipReason(true); reason != "" {
		t.Errorf("remote mode declined with %q, but two real nodes with schedds is "+
			"the one configuration where these drills can pass", reason)
	}
}
