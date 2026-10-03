package netns

import (
	"reflect"
	"testing"
)

func TestParseNftCounters(t *testing.T) {
	out := []byte(`{"nftables":[
		{"metainfo":{"version":"1.0.9"}},
		{"counter":{"family":"ip","table":"faas","name":"deny_allowlist","packets":7,"bytes":1}},
		{"counter":{"family":"ip6","table":"faas","name":"deny_allowlist","packets":2,"bytes":1}},
		{"counter":{"family":"ip","table":"faas","name":"faas_egress_denied","packets":5,"bytes":1}},
		{"counter":{"family":"ip6","table":"faas","name":"faas_egress_denied","packets":1,"bytes":1}},
		{"counter":{"family":"ip","table":"faas","name":"faas_egress_new_dst","packets":40,"bytes":1}},
		{"counter":{"family":"ip","table":"faas","name":"drop_v4_10_0_0_0_8","packets":3,"bytes":1}},
		{"counter":{"family":"ip","table":"faas","name":"faas_cap","packets":9,"bytes":1}},
		{"table":{"family":"ip","name":"faas"}}
	]}`)
	got, err := parseNftCounters(out)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]uint64{
		"deny_allowlist":     9,
		"faas_egress_denied": 6,
		EgressNewDstCounter:  40,
		"drop_v4_10_0_0_0_8": 3,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseNftCounters = %v, want %v", got, want)
	}
	if _, err := parseNftCounters([]byte("not json")); err == nil {
		t.Fatal("malformed output must be an error")
	}
}
