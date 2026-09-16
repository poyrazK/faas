package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRenderSLOMarksWakeQueueUnavailable(t *testing.T) {
	for name, render := range map[string]func(*bytes.Buffer){
		"app": func(buf *bytes.Buffer) {
			renderAppSLO(buf, api.AppSLOResponse{WakeQueueSampleStatus: api.SLOSampleStatusUnavailable})
		},
		"account": func(buf *bytes.Buffer) {
			renderAccountSLO(buf, api.AccountSLOResponse{WakeQueueSampleStatus: api.SLOSampleStatusUnavailable})
		},
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			render(&buf)
			if !strings.Contains(buf.String(), "Wake queue:  unavailable") || strings.Contains(buf.String(), "Wake queue:  0ms") {
				t.Fatalf("output does not expose tenant-safe unavailable state:\n%s", buf.String())
			}
		})
	}
}
