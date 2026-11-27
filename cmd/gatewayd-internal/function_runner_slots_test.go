package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestFunctionRunnerSlots(t *testing.T) {
	cases := []struct {
		name string
		app  state.App
		want int
	}{
		{"app has no runner", state.App{Type: state.AppTypeApp, Runtime: "node24"}, 0},
		{"function without a runtime has no runner", state.App{Type: state.AppTypeFunction}, 0},
		{"default function runs four", state.App{Type: state.AppTypeFunction, Runtime: "node24"}, 4},
		{"function_concurrency per worker", state.App{Type: state.AppTypeFunction, Runtime: "python313", Manifest: state.AppManifest{FunctionConcurrency: 8}}, 32},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := functionRunnerSlots(tc.app); got != tc.want {
				t.Fatalf("functionRunnerSlots=%d, want %d", got, tc.want)
			}
		})
	}
}
