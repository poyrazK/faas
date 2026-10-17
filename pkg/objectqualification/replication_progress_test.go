package objectqualification

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-1347: preserve complete observations under pipe chunking and log limits.
func TestReplicationProgressCaptureBounds(t *testing.T) {
	var output bytes.Buffer
	w := &replicationProgressWriter{output: &output}
	for _, part := range []string{"{\"profile\":", "{}}\n{", "}\n"} {
		if _, err := io.WriteString(w, part); err != nil {
			t.Fatal(err)
		}
	}
	if output.String() != "{\"profile\":{}}\n{}\n" {
		t.Fatal("pipe chunks changed JSONL rows", output.String())
	}
	output.Reset()
	w.bytes = api.ObjectQualificationSoakTimelineBytes - 20
	for range 3 {
		if _, err := io.WriteString(w, "{\"long\":\"record\"}\n"); err != nil {
			t.Fatal(err)
		}
	}
	if output.String() != "{\"truncated\":true}\n" || !w.truncated {
		t.Fatal("size limit did not stop capture explicitly", output.String())
	}
	output.Reset()
	w = &replicationProgressWriter{output: &output}
	if _, err := io.WriteString(w, strings.Repeat("x", api.ObjectQualificationSoakWindowBytes+1)); err != nil || !w.truncated || len(w.line) != 0 || output.String() != "{\"truncated\":true}\n" {
		t.Fatal("oversized row escaped bounded capture", err)
	}
}

type failedProgressWriter struct{}

func (failedProgressWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestReplicationProgressCaptureReportsIOFailure(t *testing.T) {
	w := &replicationProgressWriter{output: failedProgressWriter{}}
	if _, err := io.WriteString(w, "{}\n"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal("lost progress capture I/O error", err)
	}
}
