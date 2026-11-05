package preview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

type cycleIssueError struct{}

func (*cycleIssueError) Error() string   { return "cycle" }
func (e *cycleIssueError) Unwrap() error { return e }

func TestIssueErrorCausesPreserveRootWithoutInventedFrames(t *testing.T) {
	causes, incomplete := issueErrorCauses(fmt.Errorf("outer: %w", errors.New("root")))
	if incomplete || len(causes) != 1 || causes[0].Message != "root" || len(causes[0].Frames) != 0 {
		t.Fatalf("bad chain: %+v %v", causes, incomplete)
	}
	if _, incomplete = issueErrorCauses(&cycleIssueError{}); !incomplete {
		t.Fatal("cycle accepted")
	}
	if _, incomplete = issueErrorCauses(errors.Join(errors.New("one"), errors.New("two"))); !incomplete {
		t.Fatal("ambiguous aggregate accepted")
	}
	var err error = errors.New("root")
	for range 10 {
		err = fmt.Errorf("wrapper: %w", err)
	}
	if causes, incomplete = issueErrorCauses(err); !incomplete || len(causes) != 8 {
		t.Fatal("depth unbounded")
	}
}

func TestIssueCauseReporterSerializesWrappedErrors(t *testing.T) {
	var event faas.IssueEvent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	reporter, err := NewIssueReporter(IssueReporterOptions{BaseURL: server.URL, App: "exports", Token: "g_issue_test", GroupingStrategy: "cause-chain-v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer reporter.Close(context.Background())
	if reporter.CaptureException(fmt.Errorf("outer: %w", errors.New("root"))) == "" {
		t.Fatal("capture dropped")
	}
	if err = reporter.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if event.GroupingStrategy != "cause-chain-v1" || len(event.Causes) != 1 || event.Causes[0].Message != "root" || len(event.Causes[0].Frames) != 0 || len(event.Frames) == 0 {
		t.Fatalf("wrong reported chain: %+v", event)
	}
}
