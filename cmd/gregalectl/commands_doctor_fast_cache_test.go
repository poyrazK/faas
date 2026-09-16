package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCheckFastCacheRequiresComputeCacheContract(t *testing.T) {
	origRequired, origProbe := builderBaseRequiredHook, fastCacheProbeHook
	t.Cleanup(func() {
		builderBaseRequiredHook = origRequired
		fastCacheProbeHook = origProbe
	})
	builderBaseRequiredHook = func(context.Context) bool { return true }

	fastCacheProbeHook = func(context.Context) (string, error) {
		return "", errors.New("cache device differs from /srv/fc")
	}
	findings, err := checkFastCache(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Severity != doctorSeverityError ||
		!strings.Contains(findings[0].Detail, "differs") {
		t.Fatalf("failed cache findings = %+v", findings)
	}

	fastCacheProbeHook = func(context.Context) (string, error) {
		return "/var/lib/faas/cache is an XFS mount on /srv/fc", nil
	}
	findings, err = checkFastCache(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Severity != doctorSeverityOK {
		t.Fatalf("healthy cache findings = %+v", findings)
	}
}

func TestCheckFastCacheIsNotApplicableOnControlPlane(t *testing.T) {
	origRequired, origProbe := builderBaseRequiredHook, fastCacheProbeHook
	t.Cleanup(func() {
		builderBaseRequiredHook = origRequired
		fastCacheProbeHook = origProbe
	})
	builderBaseRequiredHook = func(context.Context) bool { return false }
	called := false
	fastCacheProbeHook = func(context.Context) (string, error) {
		called = true
		return "", nil
	}
	findings, err := checkFastCache(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if called || len(findings) != 1 || findings[0].Severity != doctorSeverityOK {
		t.Fatalf("control-plane cache findings = %+v called=%v", findings, called)
	}
}

func TestXFSInfoHasReflink(t *testing.T) {
	for _, tc := range []struct {
		name   string
		output string
		want   bool
	}{
		{name: "enabled", output: "crc=1 finobt=1 sparse=1 rmapbt=0 reflink=1 bigtime=1", want: true},
		{name: "disabled", output: "crc=1 finobt=1 sparse=1 rmapbt=0 reflink=0 bigtime=1", want: false},
		{name: "missing", output: "meta-data=/dev/sdb isize=512", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := xfsInfoHasReflink(tc.output); got != tc.want {
				t.Fatalf("xfsInfoHasReflink(%q) = %v, want %v", tc.output, got, tc.want)
			}
		})
	}
}
