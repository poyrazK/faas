package api

import "testing"

func TestEdgeWAFInlineBodyInspectable(t *testing.T) {
	for enc, want := range map[string]bool{
		"":           true,
		"identity":   true,
		" Identity ": true,
		"gzip":       false,
		"br":         false,
		"deflate":    false,
		"gzip, br":   false,
	} {
		if got := EdgeWAFInlineBodyInspectable(enc); got != want {
			t.Errorf("EdgeWAFInlineBodyInspectable(%q) = %v, want %v", enc, got, want)
		}
	}
}
