package jailsetup

import (
	"bytes"
	"testing"
)

func TestAwaitLaunchGateRejectsUnrecordedChildren(t *testing.T) {
	for _, token := range [][]byte{nil, {0}, {2}, {1}} {
		err := AwaitLaunchGate(bytes.NewReader(token))
		if (len(token) == 1 && token[0] == 1) != (err == nil) {
			t.Fatalf("gate token=%v err=%v", token, err)
		}
	}
}
