package flags

import (
	"slices"
	"strings"
	"testing"
)

func TestParseKillsKeepsValidUniqueBoundedKeys(t *testing.T) {
	got := ParseKills(" a-flag ,Bad Key,,b_flag,a-flag,9nope")
	if !slices.Equal(got, []string{"a-flag", "b_flag"}) {
		t.Fatalf("ParseKills = %v", got)
	}
	if ParseKills("") != nil {
		t.Fatal("empty header produced kills")
	}
	var many []string
	for i := 0; i < 150; i++ {
		many = append(many, "k"+strings.Repeat("x", i%60)+string(rune('a'+i%26)))
	}
	if got := ParseKills(strings.Join(many, ",")); len(got) > 100 {
		t.Fatalf("ParseKills kept %d keys", len(got))
	}
}

func TestFormatAndKilledKeys(t *testing.T) {
	c := Config{Flags: []Flag{{Key: "zeta", Killed: true}, {Key: "alpha"}, {Key: "beta", Killed: true}}}
	keys := KilledKeys(c)
	if !slices.Equal(keys, []string{"beta", "zeta"}) {
		t.Fatalf("KilledKeys = %v", keys)
	}
	if got := FormatKills([]string{"zeta", "beta", "zeta"}); got != "beta,zeta" {
		t.Fatalf("FormatKills = %q", got)
	}
}
