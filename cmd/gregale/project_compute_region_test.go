package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCLIProjectComputeRegionScanApplyAndPreview(t *testing.T) {
	for _, mode := range []string{"apply", "preview", "scan"} {
		t.Run(mode, func(t *testing.T) {
			sink := &decomposeSink{scanStatus: http.StatusOK, scanBody: goldenPlan, applyStatus: http.StatusOK, applyBody: goldenApply}
			srv := httptest.NewServer(sink)
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")
			_, restore := captureStdout(t)
			defer restore()
			args := []string{"--tarball", writeTarball(t), "--region", "eu-fsn"}
			var code int
			if mode == "scan" {
				code = cmdScan(args)
			} else {
				args = append(args, "--project", "--yes")
				if mode == "preview" {
					args = append(args, "--dry-run")
				}
				code = cmdDeployTarball(args)
			}
			if code != 0 {
				t.Fatalf("exit=%d", code)
			}
			if got := multipartField(sink.capturedScanMultipart, sink.scanContentType, "compute_region"); got != "eu-fsn" {
				t.Fatalf("scan region=%q", got)
			}
			if mode == "apply" {
				if got := multipartField(sink.capturedApplyMultipart, sink.applyContentType, "compute_region"); got != "eu-fsn" {
					t.Fatalf("apply region=%q", got)
				}
			} else if sink.applyCalls != 0 {
				t.Fatal("read-only command applied")
			}
		})
	}
}

func TestProjectComputeRegionRenderedAndRetryKey(t *testing.T) {
	var out bytes.Buffer
	printPlanText(&out, api.PlanResponse{CanApply: true, Workloads: []api.PlanWorkload{{Name: "customer-api", ComputeRegion: "eu-fsn"}}}, nil, false)
	if !strings.Contains(out.String(), "customer-api compute region: eu-fsn") {
		t.Fatalf("output=%s", out.String())
	}
	a, err := deployIdempotencyKey("", deployIdempotencyIntent{ProjectSlug: "project", ComputeRegion: "eu-fsn"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := deployIdempotencyKey("", deployIdempotencyIntent{ProjectSlug: "project", ComputeRegion: "eu-hel"})
	if err != nil || a == b {
		t.Fatalf("region choices share retry key: %q %q %v", a, b, err)
	}
}
