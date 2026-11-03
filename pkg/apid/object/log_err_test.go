package object

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestLogErrDropsSignedQuery(t *testing.T) {
	signed := "https://storage.googleapis.com/b/k?X-Goog-Signature=deadbeef&X-Goog-Credential=sa"
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{name: "nil"},
		{name: "plain", err: errors.New("pg: connection reset"), want: "pg: connection reset"},
		{name: "wrapped url error", err: fmt.Errorf("upload recovery: %w", &url.Error{Op: "Get", URL: signed, Err: errors.New("dial tcp: timeout")}),
			want: `upload recovery: Get "https://storage.googleapis.com/b/k": dial tcp: timeout`},
		{name: "url error without query", err: &url.Error{Op: "Put", URL: "https://example.test/b", Err: errors.New("EOF")}, want: `Put "https://example.test/b": EOF`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := logErr(tc.err)
			if got != tc.want {
				t.Fatalf("logErr = %q, want %q", got, tc.want)
			}
			if strings.Contains(got, "Signature") || strings.Contains(got, "Credential") {
				t.Fatalf("logErr kept the signature: %q", got)
			}
		})
	}
}
