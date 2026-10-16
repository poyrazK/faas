package main

import (
	"bytes"
	"github.com/onebox-faas/faas/pkg/api"
	"strings"
	"testing"
	"time"
)

func TestPromotionAdmissionDisplay(t *testing.T) {
	for _, phase := range []string{"queued", "failed", "aborted"} {
		t.Run(phase, func(t *testing.T) {
			old := osStdout
			var out bytes.Buffer
			osStdout = &out
			t.Cleanup(func() { osStdout = old })
			h := &api.ProjectPromotionHealth{Phase: phase, Generation: 3, Admission: &api.ProjectPromotionAdmission{Reason: "account_capacity", WaitDeadline: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)}, Migration: &api.ProjectReleaseCommand{Slug: "db"}}
			status := api.ProjectEnvironmentPromotionStatusResponse{PromotionID: "promotion", Health: h}
			renderProjectEnvironmentPromotionStatus(status)
			if !strings.Contains(out.String(), "wait deadline: 2026-10-15T12:00:00Z") || !strings.Contains(out.String(), "generation: 3") {
				t.Fatal("missing queue progress", out.String())
			}
			if phase == "queued" && !strings.Contains(out.String(), "waiting for capacity") {
				t.Fatal("waiting hidden", out.String())
			}
			if strings.Contains(out.String(), "healthy rounds") || strings.Contains(out.String(), "task ") {
				t.Fatal("waiting displayed as execution", out.String())
			}
		})
	}
}
