package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/internalmail"
)

func TestProducerSchedulingCLIFlagBeforeCredentials(t *testing.T) {
	for _, flag := range []string{"", "0", "true", "1 "} {
		var diagnostics bytes.Buffer
		result := run(context.Background(), func(key string) string {
			switch key {
			case internalmail.EnableEnv, internalmail.OperationsEnableEnv:
				return "1"
			case internalmail.ProducerSchedulingEnableEnv:
				return flag
			default:
				t.Fatal("disabled scheduler read private configuration", key)
				return ""
			}
		}, []string{"scheduler", "status"}, io.Discard, &diagnostics)
		if result == 0 {
			t.Fatal("disabled scheduler command succeeded")
		}
	}
	cmd, err := parseCommand([]string{"scheduler", "status"}, io.Discard)
	if err != nil || cmd.method != http.MethodGet || cmd.route != internalmail.ProducerSchedulerPath || cmd.input != nil {
		t.Fatal("invalid scheduler inspection route", cmd, err)
	}
	for _, args := range [][]string{{"scheduler", "status", "--producer", "auth"}, {"scheduler", "status", "extra"}, {"scheduler", "resume"}} {
		if _, err := parseCommand(args, io.Discard); err == nil {
			t.Fatal("unsupported command accepted", args)
		}
	}
}
