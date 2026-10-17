package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/internalmail"
)

type unreadable struct{ t *testing.T }

func (r unreadable) Read([]byte) (int, error) {
	r.t.Fatal("disabled helper read the notification")
	return 0, io.EOF
}

func TestDisabledHelperPreservesPostfixQueueWithoutReadingSecretsOrInput(t *testing.T) {
	for _, tc := range []struct{ master, reports string }{
		{"", "1"}, {"0", "1"}, {"true", "1"}, {"1", ""}, {"1", "0"}, {"1", "true"},
	} {
		var output bytes.Buffer
		status := run(t.Context(), func(key string) string {
			switch key {
			case internalmail.EnableEnv:
				return tc.master
			case internalmail.ReportsEnableEnv:
				return tc.reports
			default:
				t.Fatalf("disabled helper read %s", key)
				return ""
			}
		}, []string{"--invalid"}, unreadable{t}, &output)
		if status != temporaryFailure || !strings.HasPrefix(output.String(), "4.3.0 ") {
			t.Fatal(status, output.String())
		}
	}
}

func TestHelperRetainsReportWhenSocketIsUnavailable(t *testing.T) {
	var output bytes.Buffer
	status := run(context.Background(), func(key string) string {
		switch key {
		case internalmail.EnableEnv, internalmail.ReportsEnableEnv:
			return "1"
		case "FAAS_INTERNAL_MAIL_SOCKET":
			return t.TempDir() + "/absent.sock"
		case "FAAS_INTERNAL_MAIL_REPORT_TOKEN":
			return "private-report-token-at-least-32-bytes"
		}
		return ""
	}, []string{"--sender=<>", "--recipient=bounce+71a25d88-9a8e-425f-9e9b-b28802234faa@example.test"}, strings.NewReader("private notification contents"), &output)
	if status != temporaryFailure || strings.Contains(output.String(), "private") {
		t.Fatal(status, output.String())
	}
}
