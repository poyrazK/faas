package main

import "testing"

func TestPromotionShellCommand(t *testing.T) {
	if !promotionShellCommand([]string{"/bin/sh", "-lc", "/projectworker migration-promotion"}) {
		t.Fatal("Procfile adapter rejected the exact native command")
	}
	for _, args := range [][]string{
		nil, {"/bin/sh"}, {"/bin/sh", "-c", "/projectworker migration-promotion"},
		{"/bin/sh", "-lc", "/projectworker migration-promotion; echo fabricated"},
		{"/bin/sh", "-lc", "/projectworker migration-promotion", "extra"},
		{"/bin/sh", "-lc", "/projectworker migration-expand"},
	} {
		if promotionShellCommand(args) {
			t.Fatal("Procfile adapter accepted a different command")
		}
	}
}
