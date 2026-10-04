package devbridge

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestInspectionBoundsAndStreamingMetadata(t *testing.T) {
	i := NewInspector(2, 128)
	transport := i.Transport(roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 201, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("streamed response"))}, nil
	}))
	for n := 0; n < 3; n++ {
		request, _ := http.NewRequestWithContext(t.Context(), "POST", "http://localhost/customer/alice@example.com?api_key=secret", strings.NewReader("private body"))
		request.Header.Set("Authorization", "Bearer private-header")
		response, err := transport.RoundTrip(request)
		if err != nil {
			t.Fatal(err)
		}
		pending := i.Snapshot()
		if pending[len(pending)-1].Complete {
			t.Fatal("stream marked complete before consumption")
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || string(body) != "streamed response" {
			t.Fatal("inspection changed response")
		}
	}
	records := i.Snapshot()
	if len(records) != 2 || records[0].ID != 2 || records[1].ID != 3 {
		t.Fatal("inspection memory is not bounded")
	}
	for _, r := range records {
		if !r.Complete || r.Status != 201 || r.ResponseBytes != 17 || strings.Contains(r.Path, "secret") || strings.Contains(r.Path, "alice@example.com") {
			t.Fatalf("invalid inspection metadata: %+v", r)
		}
	}
}
