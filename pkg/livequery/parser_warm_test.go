package livequery

import (
	"testing"
	"time"
)

// adr: 20261016151947 — once WarmParser has run, parsing is fast: the
// seconds the first parse spends compiling the parser are off the
// subscriber's path.
func TestWarmParser(t *testing.T) {
	first := WarmParser()
	start := time.Now()
	if _, err := parseSelect("SELECT id, v FROM items WHERE id < $1"); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 250*time.Millisecond {
		t.Fatalf("a parse after WarmParser (%s) took %s", first, took)
	}
	if again := WarmParser(); again > 250*time.Millisecond {
		t.Fatalf("a second WarmParser took %s", again)
	}
}
