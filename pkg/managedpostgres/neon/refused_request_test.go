package neon

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestProviderJSONKeepsNeonRefusalForTheOperatorLog(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		want       error
		wantReason string
	}{
		{
			name:       "branch delete refused",
			status:     http.StatusUnprocessableEntity,
			body:       `{"request_id":"req-0f1e","code":"","message":"cannot delete the project's default branch"}`,
			want:       managedpostgres.ErrInvalid,
			wantReason: "neon DELETE /projects/p-1/branches/br-2 answered 422 request_id=req-0f1e: cannot delete the project's default branch",
		},
		{
			name:       "conflict with code",
			status:     http.StatusConflict,
			body:       `{"code":"BRANCH_HAS_CHILDREN","message":"branch has children"}`,
			want:       managedpostgres.ErrConflict,
			wantReason: "neon DELETE /projects/p-1/branches/br-2 answered 409 code=BRANCH_HAS_CHILDREN: branch has children",
		},
		{
			name:       "credentials redacted",
			status:     http.StatusBadRequest,
			body:       `{"message":"bad uri postgresql://owner:npg_s3cret@ep-x.neon.tech/db and password=npg_s3cret for owner@host\u0007"}`,
			want:       managedpostgres.ErrInvalid,
			wantReason: "neon DELETE /projects/p-1/branches/br-2 answered 400: bad uri [redacted] and [redacted] for [redacted]",
		},
		{
			name:       "unsafe identifiers dropped",
			status:     http.StatusNotFound,
			body:       `{"request_id":"req 1\n","code":"x\u0000y","message":""}`,
			want:       managedpostgres.ErrNotFound,
			wantReason: "neon DELETE /projects/p-1/branches/br-2 answered 404",
		},
		{
			name:       "not json",
			status:     http.StatusBadGateway,
			body:       `<html>bad gateway</html>`,
			want:       managedpostgres.ErrUnavailable,
			wantReason: "neon DELETE /projects/p-1/branches/br-2 answered 502",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := clientTransportProvider(clientTestTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body)), Request: r}, nil
			}))
			err := provider.doJSON(t.Context(), http.MethodDelete, "/projects/p-1/branches/br-2", nil, nil, nil, http.StatusOK)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if err.Error() != tc.want.Error() {
				t.Fatalf("error text = %q, want the bare sentinel %q", err.Error(), tc.want.Error())
			}
			if got := managedpostgres.LoggableReason(err); got != tc.wantReason {
				t.Fatalf("reason = %q\nwant     %q", got, tc.wantReason)
			}
			if strings.Contains(managedpostgres.LoggableReason(err), "s3cret") {
				t.Fatal("reason leaked a credential")
			}
		})
	}
}

func TestRedactedRefusalMessageIsBounded(t *testing.T) {
	got := redactedRefusalMessage(strings.Repeat("é", 500))
	if n := len([]rune(got)); n != maximumRefusalMessageRunes+1 || !strings.HasSuffix(got, "…") {
		t.Fatalf("bounded message has %d runes: %q", n, got)
	}
}

func TestQuietReasonChainsUnderOuterReasons(t *testing.T) {
	inner := &managedpostgres.OperatorReason{Err: managedpostgres.ErrInvalid, Reason: "neon GET /x answered 400", Quiet: true}
	outer := managedpostgres.Reasonf(inner, "historical point: read source metadata")
	if got, want := managedpostgres.LoggableReason(outer), "historical point: read source metadata: neon GET /x answered 400"; got != want {
		t.Fatalf("reason = %q, want %q", got, want)
	}
	if got, want := outer.Error(), managedpostgres.ErrInvalid.Error()+": historical point: read source metadata"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}
