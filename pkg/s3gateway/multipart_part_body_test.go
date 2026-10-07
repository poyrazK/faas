// adr: 590
package s3gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/onebox-faas/faas/pkg/state"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestMultipartPartBodyObservesOnlyVerifiedCompleteStream(t *testing.T) {
	sum := sha256.Sum256([]byte("abc"))
	digest := hex.EncodeToString(sum[:])
	for _, tc := range []struct {
		name, body, payload string
		failObservation     bool
		want                bool
	}{
		{"unsigned", "abc", "UNSIGNED-PAYLOAD", false, true},
		{"signed", "abc", digest, false, true},
		{"partial", "ab", "UNSIGNED-PAYLOAD", false, false},
		{"bad-signature-body", "abc", strings.Repeat("0", 64), false, false},
		{"journal-failure", "abc", digest, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			integrity, err := newRequestIntegrityReader(io.NopCloser(strings.NewReader(tc.body)), 3, tc.payload, http.Header{})
			if err != nil {
				t.Fatal(err)
			}
			observed := 0
			reader := newMultipartPartBodyReader(integrity, 3, func(value string) error {
				observed++
				if value != digest || integrity.remaining != 0 || integrity.err != nil {
					t.Fatal("observed unvalidated content", value)
				}
				if tc.failObservation {
					return state.ErrConflict
				}
				return nil
			})
			buf := make([]byte, 2)
			var forwarded strings.Builder
			for {
				n, err := reader.Read(buf)
				if n > 0 {
					_, _ = forwarded.Write(buf[:n])
				}
				if err != nil {
					if tc.want && !errors.Is(err, io.EOF) {
						t.Fatal(err)
					}
					break
				}
			}
			if reader.Completed() != tc.want || tc.want && (observed != 1 || forwarded.String() != "abc") || !tc.want && !tc.failObservation && observed != 0 {
				t.Fatal("unqualified body completion", reader.Completed(), observed, forwarded.String())
			}
			if _, err = reader.Read(buf); tc.want && !errors.Is(err, io.EOF) {
				t.Fatal(err)
			}
			if tc.failObservation && forwarded.String() == "abc" {
				t.Fatal("released final bytes without durable observation")
			}
		})
	}
}
