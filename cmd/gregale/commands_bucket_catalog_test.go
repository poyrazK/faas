// adr: 151
package main_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

type fakeBucketCatalog struct {
	list       api.ObjectBucketList
	listErr    error
	deleteErr  error
	deletedApp string
	deletedID  string
}

func (f *fakeBucketCatalog) ListObjectBuckets(context.Context, string) (api.ObjectBucketList, error) {
	return f.list, f.listErr
}

func (f *fakeBucketCatalog) DeleteObjectBucket(_ context.Context, app, bucket string) error {
	f.deletedApp, f.deletedID = app, bucket
	return f.deleteErr
}

func captureBucketCLI(t *testing.T, json bool) (*bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	previousOut, previousErr, previousJSON := (*main.OsStdout), (*main.OsStderr), (*main.JsonOutput)
	(*main.OsStdout), (*main.OsStderr), (*main.JsonOutput) = &stdout, &stderr, json
	t.Cleanup(func() {
		(*main.OsStdout), (*main.OsStderr), (*main.JsonOutput) = previousOut, previousErr, previousJSON
	})
	return &stdout, &stderr
}

// TestBucketListShowsIDs pins H9-141: every `bucket …` subcommand needs a
// bucket ID, so the list must print it next to the name customers chose.
func TestBucketListShowsIDs(t *testing.T) {
	stdout, _ := captureBucketCLI(t, false)
	client := &fakeBucketCatalog{list: api.ObjectBucketList{Enabled: true, Items: []api.ObjectBucket{
		{ID: "c25a9f1d-f9a6-4b06-b60f-bbc8018bb2df", Name: "uploads", Scope: "default", Region: "us-east-1", State: "ready"},
		{ID: "b5446ca5-a8d0-4896-91fe-91e0ca6520bd", Name: "assets", Scope: "default", Region: "us-east-1", State: "ready", Public: true, ServeAt: "/assets"},
	}}}
	if code := main.RunBucketList(context.Background(), client, "my-api"); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	out := stdout.String()
	for _, want := range []string{"BUCKET ID", "c25a9f1d-f9a6-4b06-b60f-bbc8018bb2df", "uploads", "b5446ca5-a8d0-4896-91fe-91e0ca6520bd", "/assets"} {
		if !strings.Contains(out, want) {
			t.Fatalf("list output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "not enabled") {
		t.Fatalf("enabled installation reported as disabled:\n%s", out)
	}
}

func TestBucketListEmptyAndDisabled(t *testing.T) {
	stdout, _ := captureBucketCLI(t, false)
	if code := main.RunBucketList(context.Background(), &fakeBucketCatalog{}, "my-api"); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if out := stdout.String(); !strings.Contains(out, "No object-storage buckets for my-api.") || !strings.Contains(out, "not enabled on this installation") {
		t.Fatalf("empty disabled output:\n%s", out)
	}
}

func TestBucketListJSON(t *testing.T) {
	stdout, _ := captureBucketCLI(t, true)
	client := &fakeBucketCatalog{list: api.ObjectBucketList{Enabled: true, Items: []api.ObjectBucket{{ID: "c25a9f1d-f9a6-4b06-b60f-bbc8018bb2df", Name: "uploads"}}}}
	if code := main.RunBucketList(context.Background(), client, "my-api"); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout.String(), `"id": "c25a9f1d-f9a6-4b06-b60f-bbc8018bb2df"`) {
		t.Fatalf("json output:\n%s", stdout.String())
	}
}

func TestBucketDeleteCallsAPI(t *testing.T) {
	stdout, _ := captureBucketCLI(t, false)
	client := &fakeBucketCatalog{}
	if code := main.RunBucketDelete(context.Background(), client, "my-api", "c25a9f1d-f9a6-4b06-b60f-bbc8018bb2df"); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if client.deletedApp != "my-api" || client.deletedID != "c25a9f1d-f9a6-4b06-b60f-bbc8018bb2df" {
		t.Fatalf("deleted %q/%q", client.deletedApp, client.deletedID)
	}
	if !strings.Contains(stdout.String(), "deleted") {
		t.Fatalf("delete output:\n%s", stdout.String())
	}
}

func TestBucketDeleteReportsAPIError(t *testing.T) {
	captureBucketCLI(t, false)
	client := &fakeBucketCatalog{deleteErr: errors.New("bucket_not_empty")}
	if code := main.RunBucketDelete(context.Background(), client, "my-api", "c25a9f1d-f9a6-4b06-b60f-bbc8018bb2df"); code == 0 {
		t.Fatal("delete error exited 0")
	}
}

func TestBucketDeleteRejectsBadArgumentsBeforeRequest(t *testing.T) {
	_, stderr := captureBucketCLI(t, false)
	for _, args := range [][]string{{"my-api"}, {"my-api", "not-a-uuid", "--yes"}, {"Bad_Slug", "c25a9f1d-f9a6-4b06-b60f-bbc8018bb2df", "--yes"}} {
		stderr.Reset()
		if code := main.CmdBucketDelete(args); code == 0 {
			t.Fatalf("cmdBucketDelete(%q) exited 0", args)
		}
		if !strings.Contains(stderr.String(), "gregale bucket delete <app> <bucket-id>") {
			t.Fatalf("cmdBucketDelete(%q) stderr:\n%s", args, stderr.String())
		}
	}
}

// TestBucketUnknownSubcommandIsNamed pins the other half of H9-141: an
// unknown subcommand used to print `bucket reconcile` usage.
func TestBucketUnknownSubcommandIsNamed(t *testing.T) {
	_, stderr := captureBucketCLI(t, false)
	if code := main.CmdBucket([]string{"versions list my-api c25a9f1d-f9a6-4b06-b60f-bbc8018bb2df"}); code == 0 {
		t.Fatal("unknown subcommand exited 0")
	}
	if got := stderr.String(); !strings.Contains(got, "unknown bucket subcommand") || strings.Contains(got, "usage: gregale bucket reconcile") {
		t.Fatalf("unknown subcommand stderr:\n%s", got)
	}
}
