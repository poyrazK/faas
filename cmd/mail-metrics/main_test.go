package main

import (
	"bytes"
	"io"
	"testing"

	"github.com/onebox-faas/faas/pkg/internalmail"
)

func TestDisabledMonitoringHelperDoesNotReadSecretsOrArguments(t *testing.T) {
	for _, tc := range []struct{ master, reports string }{{"", "1"}, {"0", "1"}, {"true", "1"}, {"1", "0"}, {"1", "true"}} {
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
		}, []string{"--invalid"}, io.Discard)
		if status != 0 {
			t.Fatal(status)
		}
	}
}

func TestRelayHealthHelperRejectsNonLoopbackBeforeCollection(t *testing.T) {
	values := map[string]string{
		internalmail.EnableEnv: "1", internalmail.ReportsEnableEnv: "1", internalmail.BackpressureEnableEnv: "1",
		"FAAS_INTERNAL_MAIL_SOCKET": "/does/not/exist/mail.sock", "FAAS_INTERNAL_MAIL_REPORT_TOKEN": "private-relay-observer-token-32-bytes",
		"FAAS_INTERNAL_MAIL_SMTP_ADDR": "relay.external.test:25",
	}
	var output bytes.Buffer
	if status := run(t.Context(), func(key string) string { return values[key] }, nil, &output); status != 1 || output.String() != "internal mail metrics collection failed\n" {
		t.Fatal("helper accepted external relay or leaked configuration", status, output.String())
	}
}
