package preview

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestIssueTrimmingIgnoresRouteAndStatus(t *testing.T) {
	frames := make([]faas.IssueFrame, 32)
	for i := range frames {
		frames[i] = faas.IssueFrame{File: strings.Repeat("x", 435), Function: strings.Repeat("f", 150), Line: 1, InApp: true}
	}
	event := faas.IssueEvent{OccurredAt: time.Now().UTC(), ExceptionType: "Error", Frames: frames, Causes: []faas.IssueCause{{ExceptionType: "Error", Frames: frames}, {ExceptionType: "Error", Frames: frames}}}
	without, err := marshalBoundedIssueEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	event.Route, event.HTTPStatus = strings.Repeat("\x01", 256), 500
	with, err := marshalBoundedIssueEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	var a, b faas.IssueEvent
	if err := json.Unmarshal(without, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(with, &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Causes, b.Causes) || a.CausesIncomplete != b.CausesIncomplete || !reflect.DeepEqual(a.Frames, b.Frames) {
		t.Fatal("route changed retained grouping evidence")
	}
}
