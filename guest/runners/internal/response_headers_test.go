package internal

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

func TestApplyResponseHeadersContentType(t *testing.T) {
	tests := []struct {
		name string
		src  HeaderValues
		want string
	}{
		{name: "json", src: HeaderValues{"content-type": {"application/json"}}, want: "application/json"},
		{name: "text", src: HeaderValues{"Content-Type": {"text/plain; charset=utf-8"}}, want: "text/plain; charset=utf-8"},
		{name: "binary", src: HeaderValues{"CONTENT-TYPE": {"image/png"}}, want: "image/png"},
		{name: "missing", src: HeaderValues{"X-Test": {"yes"}}, want: "application/octet-stream"},
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

// adr: 999
func TestHeaderValuesAcceptStringOrList(t *testing.T) {
	var got HeaderValues
	if err := json.Unmarshal([]byte(`{"content-type":"text/plain","set-cookie":["a=1","b=2"]}`), &got); err != nil {
		t.Fatal(err)
	}
	want := HeaderValues{"content-type": {"text/plain"}, "set-cookie": {"a=1", "b=2"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decoded %v, want %v", got, want)
	}
	if err := json.Unmarshal([]byte(`{"x-count":3}`), &got); err == nil {
		t.Fatal("a number header value decoded; want an error")
	}
	dst := make(http.Header)
	ApplyResponseHeaders(dst, want)
	if cookies := dst.Values("Set-Cookie"); !reflect.DeepEqual(cookies, []string{"a=1", "b=2"}) {
		t.Fatalf("Set-Cookie lines = %v, want both", cookies)
	}
}

// adr: 999
func TestRequestHeadersJoinRepeatedValues(t *testing.T) {
	h := http.Header{}
	h.Add("Accept", "text/html")
	h.Add("Accept", "application/json")
	h.Add("Cookie", "a=1")
	h.Add("Cookie", "b=2")
	h.Set("X-Trace-Id", "abc")
	got := RequestHeaders(h)
	want := map[string]string{"Accept": "text/html, application/json", "Cookie": "a=1; b=2", "X-Trace-Id": "abc"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RequestHeaders = %v, want %v", got, want)
	}
}
