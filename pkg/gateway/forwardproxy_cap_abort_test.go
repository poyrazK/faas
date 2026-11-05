package gateway

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
)

// TestForwarder_StreamPastBodyCapAborts pins Hunt 11 H11-36: a streamed
// response that passed the plan's body cap after its 200 went out came back
// as HTTP 200 with the cap's worth of data, then a 413 problem document
// appended to the payload, and a clean end of body (curl exit 0).
func TestForwarder_StreamPastBodyCapAborts(t *testing.T) {
	tests := []struct {
		name      string
		chunks    []string
		wantAbort bool
	}{
		{"under the cap streams whole", []string{"aaaa", "bbbb"}, false},
		{"at the cap streams whole", []string{"aaaa", "bbbbbb"}, false},
		{"past the cap aborts", []string{"aaaa", "bbbb", "cccc"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frames := []*vmmdpb.ForwardHTTPStreamResponse{
				{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{
					Status:  http.StatusOK,
					Headers: []*vmmdpb.Header{{Name: "Content-Type", Value: "application/octet-stream"}},
				}}},
			}
			for _, c := range tt.chunks {
				frames = append(frames, &vmmdpb.ForwardHTTPStreamResponse{Frame: &vmmdpb.ForwardHTTPStreamResponse_BodyChunk{BodyChunk: []byte(c)}})
			}
			nodes := singleClientLookup{cli: &failAfterFramesClient{stream: &failAfterFramesStream{frames: frames, err: io.EOF}}}
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// The same wrapper setupStreamingWriter installs, with a 10-byte cap.
				cw := &capWriter{ResponseWriter: w, cap: 10, disabled: &atomic.Bool{}, onCap: func() {
					api.WriteProblem(w, api.NewProblem(http.StatusRequestEntityTooLarge, api.CodeStreamingNotAvailable,
						"streaming response exceeded plan cap", "capped at 10 bytes per response"))
				}}
				fwdOnceWithEvents(cw, r, nodes, log, Target{NodeID: "n1"}, nil)
			}))
			t.Cleanup(srv.Close)
			srv.Config.ErrorLog = nil

			resp, err := srv.Client().Get(srv.URL + "/big")
			if err != nil {
				if !tt.wantAbort {
					t.Fatalf("GET: %v", err)
				}
				return
			}
			defer func() { _ = resp.Body.Close() }()
			body, readErr := io.ReadAll(resp.Body)
			if strings.Contains(string(body), `"type"`) {
				t.Fatalf("problem document appended to committed %d body: %q", resp.StatusCode, body)
			}
			if tt.wantAbort && readErr == nil {
				t.Fatalf("over-cap body %q read back cleanly under %d; want a transport error", body, resp.StatusCode)
			}
			if !tt.wantAbort && (readErr != nil || string(body) != strings.Join(tt.chunks, "")) {
				t.Fatalf("body %q, err %v; want %q", body, readErr, strings.Join(tt.chunks, ""))
			}
		})
	}
}
