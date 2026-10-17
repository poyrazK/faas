package main

import (
	"flag"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWriteAdmissionFlagsValidateBeforeOpeningStore(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		minFree int64
		bad     bool
	}{
		{"defaults", nil, 0, false},
		{"custom free floor", nil, 2 << 30, false},
		{"disabled", []string{"--write-admission=false"}, 1, false},
		{"bad hysteresis", []string{"--admit-lag-low=4096"}, 1, true},
		{"above hard ceiling", []string{"--admit-replay-high=999999999999999"}, 1, true},
		{"resume below floor", []string{"--admit-resume-free-bytes=1024"}, 2 << 30, true},
		{"negative floor", nil, -1, true},
		{"overflow floor", nil, api.MaxObjectStoragePolicyValue, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flags := flag.NewFlagSet(tc.name, flag.ContinueOnError)
			o := writeAdmissionFlags(flags)
			if err := flags.Parse(tc.args); err != nil {
				t.Fatal(err)
			}
			err := resolveWriteAdmission(o, tc.minFree)
			if (err != nil) != tc.bad {
				t.Fatal(o, err)
			}
			if err == nil && o.ResumeFreeBytes <= o.MinFreeBytes {
				t.Fatal("invalid recovery watermark", o)
			}
		})
	}
}
