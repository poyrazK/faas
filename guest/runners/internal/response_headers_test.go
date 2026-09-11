package internal

import (
	"net/http"
	"testing"
)

func TestApplyResponseHeadersContentType(t *testing.T) {
	tests := []struct {
		name string
		src  map[string]string
		want string
	}{
		{name: "json", src: map[string]string{"content-type": "application/json"}, want: "application/json"},
		{name: "text", src: map[string]string{"Content-Type": "text/plain; charset=utf-8"}, want: "text/plain; charset=utf-8"},
		{name: "binary", src: map[string]string{"CONTENT-TYPE": "image/png"}, want: "image/png"},
		{name: "missing", src: map[string]string{"X-Test": "yes"}, want: "application/octet-stream"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dst := make(http.Header)
			ApplyResponseHeaders(dst, test.src)
			if got := dst.Get("Content-Type"); got != test.want {
				t.Fatalf("Content-Type = %q, want %q", got, test.want)
			}
		})
	}
}
