package imaged

// adr: 430

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Exercises the actual process/pipe boundary with an executable fixture.
// This is not a Grype or native ext4 acceptance test.
func TestScanCommandRefusesOversizedStreamsWithoutEchoingData(t *testing.T) {
	for _, stream := range []string{"stdout", "stderr"} {
		t.Run(stream, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "scanner")
			redirect := ""
			if stream == "stderr" {
				redirect = " >&2"
			}
			script := "#!/bin/sh\ni=0\nwhile [ $i -lt 4000 ]; do printf 'PRIVATE-SCAN-DIAGNOSTIC'" + redirect + "; i=$((i+1)); done\n"
			if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			_, _, err := runBoundedScanCommand(ctx, api.ApplicationStandardScanMaxErrorBytes, path)
			if err == nil || strings.Contains(err.Error(), "PRIVATE-SCAN-DIAGNOSTIC") {
				t.Fatalf("oversized output accepted or diagnostic echoed: %v", err)
			}
		})
	}
}

func TestPrepareGrypeSourceRejectsOpaqueNonExt4(t *testing.T) {
	file := filepath.Join(t.TempDir(), "rootfs.ext4")
	if err := os.WriteFile(file, []byte("opaque producer fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := prepareGrypeSource(t.Context(), file); err == nil {
		t.Fatal("opaque ext4 treated as a catalogable filesystem")
	}
}
