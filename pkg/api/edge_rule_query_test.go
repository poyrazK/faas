package api

import (
	"reflect"
	"testing"
)

// adr: 20261107190000 — edge rules read the query as apps do.
func TestParseEdgeRuleQueryKeepsSemicolonPairs(t *testing.T) {
	got, err := ParseEdgeRuleQuery("id=1%20OR%201=1;--&page=2&tag=a&tag=b&flag&=empty-key&&x=a+b")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"id": {"1 OR 1=1;--"}, "page": {"2"}, "tag": {"a", "b"}, "flag": {""}, "": {"empty-key"}, "x": {"a b"},
	}
	if !reflect.DeepEqual(map[string][]string(got), want) {
		t.Fatalf("ParseEdgeRuleQuery = %v, want %v", got, want)
	}
	bad, err := ParseEdgeRuleQuery("a=%zz&b=1")
	if err == nil || bad.Get("a") != "%zz" || bad.Get("b") != "1" {
		t.Fatalf("undecodable escape = %v, %v; want the pair kept as written and an error", bad, err)
	}
}
