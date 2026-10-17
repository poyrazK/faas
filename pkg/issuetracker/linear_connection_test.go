package issuetracker

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestVerifyLinearConnection(t *testing.T) {
	const team = "44444444-4444-4444-8444-444444444444"
	const body = `{"data":{"organization":{"id":"11111111-1111-4111-8111-111111111111"},"viewer":{"id":"22222222-2222-4222-8222-222222222222"},"team":{"id":"44444444-4444-4444-8444-444444444444","archivedAt":null}}}`
	for _, tc := range []struct {
		name, body string
		ok         bool
	}{
		{"verified", body, true},
		{"archived", strings.Replace(body, `"archivedAt":null`, `"archivedAt":"2026-10-14"`, 1), false},
		{"missing archive", strings.Replace(body, `,"archivedAt":null`, "", 1), false},
		{"different team", strings.Replace(body, team, "33333333-3333-4333-8333-333333333333", 1), false},
		{"partial graphql", strings.Replace(body, `{"data":`, `{"errors":[{"message":"secret diagnostic"}],"data":`, 1), false},
		{"missing identity", `{"data":{}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() != "https://api.linear.app/graphql" || r.Header.Get("Authorization") != "lin_api_secret" {
					t.Fatal("wrong destination or authentication")
				}
				return response(200, tc.body), nil
			})})
			result, err := c.VerifyLinearConnection(context.Background(), "lin_api_secret", team)
			if (err == nil) != tc.ok || tc.ok && result.TeamID != team {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if err != nil && strings.Contains(err.Error(), "secret diagnostic") {
				t.Fatal("provider diagnostic leaked")
			}
			_, err = c.VerifyLinearConnection(context.Background(), "lin_api_secret", "invalid")
			if err == nil || calls != 1 {
				t.Fatal("invalid team dispatched")
			}
		})
	}
}
