package e2e_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func TestGitOpsSourceFixturePinsArchivesAndRecoversOnSameConnection(t *testing.T) {
	f := newGitOpsSourceFixture(t)
	f.setAccount("owner")
	definition := api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: "shop", Environment: "production",
		Workloads: map[string]api.EnvironmentWorkload{"api": {App: "shop-api", Variables: map[string]string{"MODE": "original"}}}}
	first, second := strings.Repeat("a", 40), strings.Repeat("b", 40)
	f.addFiles(t, first, definition, map[string][]byte{"jobs/report/commit.txt": []byte(first)})
	workload := definition.Workloads["api"]
	workload.Variables = map[string]string{"MODE": "changed"}
	definition.Workloads["api"] = workload
	f.add(t, second, definition)
	connection, err := grpc.NewClient("unix://"+f.socket, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	client := githubdpb.NewGithubdClient(connection)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for _, tc := range []struct{ ref, sha, value string }{
		{"refs/heads/main", second, "changed"}, {first, first, "original"},
	} {
		stream, err := client.StreamSourceRef(ctx, &githubdpb.StreamSourceRefRequest{
			AccountId: "owner", InstallationId: 42, RepoFullName: "example/shop", Ref: tc.ref, MaxArchiveBytes: 1 << 20})
		if err != nil {
			t.Fatal(err)
		}
		var archive bytes.Buffer
		var resolved string
		for {
			chunk, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			archive.Write(chunk.Data)
			if chunk.ResolvedCommitSha != "" {
				resolved = chunk.ResolvedCommitSha
				if chunk.BytesStreamed != int64(archive.Len()) || chunk.Truncated {
					t.Fatal("fixture returned inconsistent terminal provenance")
				}
			}
		}
		if tc.sha == first {
			gz, err := gzip.NewReader(bytes.NewReader(archive.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			tr := tar.NewReader(gz)
			found := false
			for {
				hdr, err := tr.Next()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if hdr.Name == "shop-"+first+"/jobs/report/commit.txt" {
					content, err := io.ReadAll(tr)
					if err != nil || string(content) != first {
						t.Fatal("GitHub build context differs from reviewed commit")
					}
					found = true
				}
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			if !found {
				t.Fatal("GitHub provider omitted the source build context")
			}
		}
		desired, err := environmentgitops.ReadGitDefinition(bytes.NewReader(archive.Bytes()), "environments/production.yaml", 1<<20)
		if err != nil || resolved != tc.sha || desired.Definition.Workloads["api"].Variables["MODE"] != tc.value {
			t.Fatalf("immutable archive %s: sha=%s desired=%+v err=%v", tc.ref, resolved, desired, err)
		}
	}
	for _, tc := range []struct {
		name, account string
		unavailable   bool
		code          codes.Code
	}{
		{"account isolation", "other", false, codes.PermissionDenied},
		{"source outage", "owner", true, codes.Unavailable},
		{"recovery", "owner", false, codes.OK},
	} {
		f.setUnavailable(tc.unavailable)
		stream, err := client.StreamSourceRef(ctx, &githubdpb.StreamSourceRefRequest{
			AccountId: tc.account, InstallationId: 42, RepoFullName: "example/shop", Ref: first, MaxArchiveBytes: 1 << 20})
		if err == nil {
			_, err = stream.Recv()
		}
		if status.Code(err) != tc.code {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
}
