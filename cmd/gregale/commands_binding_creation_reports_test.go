package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingrun"
	"net/url"
	"testing"
)

func TestBindingCreationFailureReports(t *testing.T) {
	for _, graph := range []bool{false, true} {
		for _, attempted := range []bool{false, true} {
			for _, plain := range []bool{false, true} {
				oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
				var out, stderr bytes.Buffer
				osStdout, osStderr, jsonOutput = &out, &stderr, !plain
				id := "11111111-1111-4111-8111-111111111111"
				app, p, env := "api", "", ""
				if graph {
					app, p, env = "", "project", "production"
				}
				err := &bindingrun.CreationError{RunID: id, Attempted: attempted, Err: errors.New("original creation failure")}
				code := renderBindingCreationFailure(id, app, p, env, err)
				osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON
				if code != 1 {
					t.Fatalf("code=%d", code)
				}
				if plain {
					if !bytes.Contains(stderr.Bytes(), []byte(id)) || !bytes.Contains(stderr.Bytes(), []byte("original creation failure")) {
						t.Fatal(stderr.String())
					}
					continue
				}
				var report bindingCreationFailureReport
				if e := json.Unmarshal(out.Bytes(), &report); e != nil {
					t.Fatal(e)
				}
				if report.RunID != id || report.CreateAttempted != attempted || report.Passed || report.Status != "creation_failed" || report.App != app || report.Project != p || report.Environment != env || report.Recovery == "" {
					t.Fatalf("incorrect report: %+v", report)
				}
			}
		}
	}
}

func TestBindingCreationFailureClassificationAndRedaction(t *testing.T) {
	for _, scenario := range []struct {
		cause error
		code  int
	}{
		{context.Canceled, 130},
		{&APIError{Problem: api.Problem{Status: 404, Detail: "fp_live_private_token"}}, 4},
		{&url.Error{Op: "POST", URL: "https://api.invalid", Err: errors.New("connection refused fp_live_private_token")}, 3},
	} {
		oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
		var out, stderr bytes.Buffer
		osStdout, osStderr, jsonOutput = &out, &stderr, true
		code := renderBindingCreationFailure("11111111-1111-4111-8111-111111111111", "api", "", "", &bindingrun.CreationError{RunID: "11111111-1111-4111-8111-111111111111", Attempted: true, Err: scenario.cause})
		osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON
		if code != scenario.code || bytes.Contains(out.Bytes(), []byte("fp_live_private_token")) {
			t.Fatalf("classification/redaction: code=%d output=%s", code, out.String())
		}
	}
}
