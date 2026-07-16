package main

import "testing"

func TestRunExitCodes(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
	}{
		{"no args prints help", nil, 0},
		{"version", []string{"version"}, 0},
		{"version flag", []string{"--version"}, 0},
		{"help", []string{"help"}, 0},
		{"unknown command", []string{"frobnicate"}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := run(tt.args); got != tt.want {
				t.Errorf("run(%v) = %d, want %d", tt.args, got, tt.want)
			}
		})
	}
}
