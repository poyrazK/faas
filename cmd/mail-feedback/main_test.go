package main

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/internalmail"
)

type unreadable struct{ t *testing.T }

func (r unreadable) Read([]byte) (int, error) {
	r.t.Fatal("disabled helper read feedback")
	return 0, io.EOF
}

func TestDisabledFeedbackHelperReadsNoSecretsOrMailAndRetainsInput(t *testing.T) {
	for _, tc := range []struct{ master, feedback string }{
		{"", "1"}, {"0", "1"}, {"true", "1"}, {"1", ""}, {"1", "0"}, {"1", "true"},
	} {
		var output bytes.Buffer
		status := run(t.Context(), func(key string) string {
			switch key {
			case internalmail.EnableEnv:
				return tc.master
			case internalmail.FeedbackEnableEnv:
				return tc.feedback
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

func TestFeedbackHelperFailureOutputDoesNotContainCredentialsOrReport(t *testing.T) {
	var output bytes.Buffer
	status := run(t.Context(), func(key string) string {
		switch key {
		case internalmail.EnableEnv, internalmail.FeedbackEnableEnv:
			return "1"
		case "FAAS_INTERNAL_MAIL_SOCKET":
			return t.TempDir() + "/absent.sock"
		case "FAAS_INTERNAL_MAIL_FEEDBACK_SOURCE":
			return "provider"
		case "FAAS_INTERNAL_MAIL_FEEDBACK_TOKEN":
			return "private-source-token-at-least-32-bytes"
		}
		return ""
	}, nil, strings.NewReader("private feedback body"), &output)
	if status != temporaryFailure || strings.Contains(output.String(), "private") {
		t.Fatal(status, output.String())
	}
}
