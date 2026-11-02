package api

import (
	"net/http"
	"testing"
)

func TestEdgeRuleMethodOverride(t *testing.T) {
	for _, tc := range []struct {
		name, method, header, value, query, want string
	}{
		{name: "none", method: "POST"},
		{name: "x-http-method-override", method: "POST", header: "X-HTTP-Method-Override", value: "DELETE", want: "DELETE"},
		{name: "x-http-method lower case", method: "POST", header: "X-HTTP-Method", value: " put ", want: "PUT"},
		{name: "x-method-override", method: "POST", header: "X-Method-Override", value: "PATCH", want: "PATCH"},
		{name: "query _method", method: "POST", query: "a=1&_method=delete", want: "DELETE"},
		{name: "same as request method", method: "POST", header: "X-HTTP-Method-Override", value: "POST"},
		{name: "unknown method ignored", method: "POST", header: "X-HTTP-Method-Override", value: "PURGE"},
		{name: "other parameter named like it", method: "POST", query: "x_method=DELETE"},
		{name: "unparseable query", method: "POST", query: "_method=%zz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			header := http.Header{}
			if tc.header != "" {
				header.Set(tc.header, tc.value)
			}
			if got := EdgeRuleMethodOverride(tc.method, header, tc.query); got != tc.want {
				t.Fatalf("EdgeRuleMethodOverride = %q, want %q", got, tc.want)
			}
		})
	}
}
