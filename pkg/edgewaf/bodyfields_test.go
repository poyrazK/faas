package edgewaf

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
)

func TestBoundBodyFields(t *testing.T) {
	for _, tc := range []struct {
		name, body, contentType string
		n                       int
		want                    string
		wantCut                 bool
	}{
		{name: "json under the bound", body: `{"a":1,"b":"x"}`, contentType: "application/json", n: 2, want: `{"a":1,"b":"x"}`},
		{name: "json cut after n leaves", body: `{"a":1,"b":"x","c":true}`, contentType: "application/json", n: 2, want: `{"a":1,"b":"x"`, wantCut: true},
		{name: "keys are not leaves", body: `{"aaaa":{"bbbb":{"cccc":1}},"d":2}`, contentType: "application/json", n: 1, want: `{"aaaa":{"bbbb":{"cccc":1}}`, wantCut: true},
		{name: "array elements are leaves", body: `{"ids":[1,2,3,4]}`, contentType: "application/json; charset=utf-8", n: 3, want: `{"ids":[1,2,3`, wantCut: true},
		{name: "objects in arrays", body: `[{"id":1,"n":"a"},{"id":2,"n":"b"}]`, contentType: "application/json", n: 3, want: `[{"id":1,"n":"a"},{"id":2`, wantCut: true},
		{name: "null and false count", body: `{"a":null,"b":false,"c":0}`, contentType: "application/json", n: 2, want: `{"a":null,"b":false`, wantCut: true},
		{name: "vendor json type", body: `{"a":1,"b":2}`, contentType: "application/vnd.api+json", n: 1, want: `{"a":1`, wantCut: true},
		{name: "json already cut short", body: `{"a":1,"b":"unterminated`, contentType: "application/json", n: 5, want: `{"a":1,"b":"unterminated`},
		{name: "invalid json left whole", body: `not json at all`, contentType: "application/json", n: 1, want: `not json at all`},
		{name: "form cut after n pairs", body: "a=1&b=2&c=3", contentType: "application/x-www-form-urlencoded", n: 2, want: "a=1&b=2", wantCut: true},
		{name: "form under the bound", body: "a=1&b=2", contentType: "application/x-www-form-urlencoded", n: 2, want: "a=1&b=2"},
		{name: "plain text is one argument", body: "a=1&b=2&c=3", contentType: "text/plain", n: 1, want: "a=1&b=2&c=3"},
		{name: "no content type", body: `{"a":1,"b":2}`, n: 1, want: `{"a":1,"b":2}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, cut := boundBodyFields([]byte(tc.body), tc.contentType, tc.n)
			if string(got) != tc.want || cut != tc.wantCut {
				t.Errorf("boundBodyFields = %q (cut %v), want %q (cut %v)", got, cut, tc.want, tc.wantCut)
			}
		})
	}
}

// TestCheckInlineBoundsBodyFields: a strict in-path check scores at most
// api.EdgeWAFMaxInlineBodyFields fields of the body; an attack in a later
// field is left to the sampled check.
func TestCheckInlineBoundsBodyFields(t *testing.T) {
	i := New(&recordingObserver{}, nil)
	body := func(attackAt int) string {
		fields := make([]string, 40)
		for f := range fields {
			v := fmt.Sprintf("value %d", f)
			if f == attackAt {
				v = "<script>alert(document.cookie)</script>"
			}
			fields[f] = fmt.Sprintf("%q:%q", fmt.Sprintf("f%d", f), v)
		}
		return "{" + strings.Join(fields, ",") + "}"
	}
	for _, tc := range []struct {
		name     string
		attackAt int
		want     string
	}{
		{name: "attack in the first fields", attackAt: 1, want: gateway.WAFInlineDetected},
		{name: "attack in the last scored field", attackAt: api.EdgeWAFMaxInlineBodyFields - 1, want: gateway.WAFInlineDetected},
		{name: "attack past the field bound", attackAt: api.EdgeWAFMaxInlineBodyFields, want: gateway.WAFInlineClean},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := body(tc.attackAt)
			if len(b) > api.EdgeWAFMaxInlineBodyBytes {
				t.Fatalf("test body is %d bytes, past the byte bound", len(b))
			}
			if res := i.CheckInline(sample(http.MethodPost, "/api/comments", nil, b)); res.Outcome != tc.want {
				t.Fatalf("outcome = %s (rules %v), want %s", res.Outcome, res.RuleIDs, tc.want)
			}
		})
	}
}
