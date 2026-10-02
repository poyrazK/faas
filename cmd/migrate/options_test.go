package main

import (
	"io"
	"testing"
)

func TestLedgerRecoveryOptions(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		recovery string
		invalid  bool
	}{
		{"ordinary apply", nil, "", false},
		{"leader status", []string{"-leader", "-status"}, "", false},
		{"prepare", []string{"-prepare-ledger-recovery"}, "prepare", false},
		{"preview", []string{"-ledger-recovery-plan"}, "preview", false},
		{"apply", []string{"-ledger-recovery-apply", "reviewed-hash"}, "apply", false},
		{"empty approval", []string{"-ledger-recovery-apply="}, "", true},
		{"mixed repair modes", []string{"-prepare-ledger-recovery", "-ledger-recovery-plan"}, "", true},
		{"repair and status", []string{"-ledger-recovery-plan", "-status"}, "", true},
		{"repair and leader", []string{"-prepare-ledger-recovery", "-leader"}, "", true},
		{"repair and waiter", []string{"-ledger-recovery-plan", "-wait-for-migrations"}, "", true},
		{"waiter and status", []string{"-wait-for-migrations", "-status"}, "", true},
		{"positional", []string{"anything"}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			options, err := parseMigrationOptions(tc.args, io.Discard)
			if (err != nil) != tc.invalid {
				t.Fatalf("parse error=%v, invalid=%v", err, tc.invalid)
			}
			if !tc.invalid && options.Recovery != tc.recovery {
				t.Fatalf("mode=%q, want %q", options.Recovery, tc.recovery)
			}
		})
	}
}
