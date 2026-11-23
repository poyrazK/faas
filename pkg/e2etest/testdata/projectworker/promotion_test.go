package main

import "testing"

func TestPromotionShellCommand(t *testing.T) {
	for _, flag := range []string{"-c", "-lc"} {
		if !promotionShellCommand([]string{"/bin/sh", flag, "/projectworker migration-promotion"}) {
			t.Fatalf("Procfile adapter rejected the exact native command with %s", flag)
		}
	}
	for _, args := range [][]string{
		nil, {"/bin/sh"}, {"/bin/sh", "-l", "/projectworker migration-promotion"},
		{"/bin/sh", "-lc", "/projectworker migration-promotion; echo fabricated"},
		{"/bin/sh", "-lc", "/projectworker migration-promotion", "extra"},
		{"/bin/sh", "-lc", "/projectworker migration-expand"},
	} {
		if promotionShellCommand(args) {
			t.Fatal("Procfile adapter accepted a different command")
		}
	}
}
