package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/s3gateway"
)

func TestReplicationWorkFlagsAndProgress(t *testing.T) {
	primary, directory, _ := followFixture(t, false)
	if _, err := primary.Put(t.Context(), "test-bucket", "new", strings.NewReader("data"), 4, objectnode.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	h, err := s3gateway.NewDiskHandler(s3gateway.DiskConfig{Store: primary, AccessKeyID: "work-cli", SecretAccessKey: "work-cli", Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	t.Setenv("FAAS_OBJECT_NODE_ACCESS_KEY_ID", "work-cli")
	t.Setenv("FAAS_OBJECT_NODE_SECRET_ACCESS_KEY", "work-cli")
	args := []string{"replica-catch-up", "--data-dir", directory, "--source", server.URL}
	for _, flags := range [][]string{
		{"--work-bytes-per-second", "0", "--work-entries-per-second", "0"},
		{"--work-bytes-per-second", "0"},
		{"--work-bytes-per-second", "-1"},
		{"--work-bytes-per-second", strconv.FormatInt(api.ObjectNodeReplicationMaxWorkBytes+1, 10)},
		{"--work-entries-per-second", "0"},
		{"--work-entries-per-second", strconv.Itoa(api.ObjectNodeReplicationMaxWorkEntries + 1)},
	} {
		if err = run(t.Context(), append(append([]string{}, args...), flags...), io.Discard); !errors.Is(err, objectnode.ErrInvalid) {
			t.Fatal("invalid pacing configuration accepted", flags, err)
		}
	}
	var output bytes.Buffer
	if err = run(t.Context(), args, &output); err != nil {
		t.Fatal(err)
	}
	var progress objectnode.ReplicationProgress
	if err = json.Unmarshal(output.Bytes(), &progress); err != nil || !progress.CaughtUp || progress.WorkBytes <= progress.BytesTransferred || progress.WorkWaitMS < 0 {
		t.Fatal("CLI omitted paced local work", output.String(), err)
	}
}
