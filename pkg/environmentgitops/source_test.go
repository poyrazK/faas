package environmentgitops_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops"
)

const manifest = "api_version: gregale.dev/environment/v1\nproject: shop\nenvironment: production\nworkloads:\n  api:\n    app: shop-api\n"

func gitArchive(t *testing.T, headers []*tar.Header, bodies []string) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	archive := tar.NewWriter(gz)
	for i, header := range headers {
		if header.Typeflag == tar.TypeReg {
			header.Size = int64(len(bodies[i]))
		}
		if err := archive.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeReg {
			if _, err := archive.Write([]byte(bodies[i])); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestReadGitBuildDefinitionRequiresExactMemberDockerfile(t *testing.T) {
	archive := gitArchive(t, []*tar.Header{
		{Name: "shop-sha/environments/production.yaml", Typeflag: tar.TypeReg},
		{Name: "shop-sha/Dockerfile", Typeflag: tar.TypeReg},
		{Name: "shop-sha/apps/api/package.json", Typeflag: tar.TypeReg},
		{Name: "shop-sha/apps/api/deploy/Dockerfile", Typeflag: tar.TypeReg},
	}, []string{manifest, "FROM scratch", `{}`, "FROM scratch"})
	for _, tc := range []struct {
		name   string
		source api.EnvironmentWorkloadSource
		valid  bool
	}{
		{"source member", api.EnvironmentWorkloadSource{Kind: "source", Directory: "apps/api"}, true},
		{"exact Dockerfile", api.EnvironmentWorkloadSource{Kind: "dockerfile", Directory: "apps/api", Dockerfile: "deploy/Dockerfile"}, true},
		{"missing member", api.EnvironmentWorkloadSource{Kind: "source", Directory: "apps/missing"}, false},
		{"repository fallback forbidden", api.EnvironmentWorkloadSource{Kind: "dockerfile", Directory: "apps/api", Dockerfile: "Dockerfile"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := environmentgitops.ReadGitBuildDefinition(bytes.NewReader(archive), "environments/production.yaml", int64(len(archive)), []api.EnvironmentWorkloadSource{tc.source})
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
}

func TestReadGitDefinitionVerifiesArchiveAndManifest(t *testing.T) {
	archive := gitArchive(t, []*tar.Header{{Name: "shop-sha/environments/production.yaml", Typeflag: tar.TypeReg}}, []string{manifest})
	desired, err := environmentgitops.ReadGitDefinition(bytes.NewReader(archive), "environments/production.yaml", int64(len(archive)))
	if err != nil || desired.Definition.Workloads["api"].App != "shop-api" {
		t.Fatalf("definition: %+v %v", desired, err)
	}
	for _, tc := range []struct {
		name  string
		bytes []byte
		limit int64
	}{
		{"compressed size", archive, int64(len(archive) - 1)},
		{"truncated checksum", archive[:len(archive)-4], int64(len(archive))},
		{"concatenated archive", append(bytes.Clone(archive), archive...), 1 << 20},
		{"duplicate manifest", gitArchive(t, []*tar.Header{{Name: "root/environments/production.yaml", Typeflag: tar.TypeReg}, {Name: "root/environments/production.yaml", Typeflag: tar.TypeReg}}, []string{manifest, manifest}), 1 << 20},
		{"symlink manifest", gitArchive(t, []*tar.Header{{Name: "root/environments/production.yaml", Typeflag: tar.TypeSymlink, Linkname: "../secret"}}, []string{""}), 1 << 20},
		{"traversal", gitArchive(t, []*tar.Header{{Name: "root/../environments/production.yaml", Typeflag: tar.TypeReg}}, []string{manifest}), 1 << 20},
		{"multiple roots", gitArchive(t, []*tar.Header{{Name: "one/environments/production.yaml", Typeflag: tar.TypeReg}, {Name: "two/other", Typeflag: tar.TypeReg}}, []string{manifest, "other"}), 1 << 20},
		{"malformed manifest", gitArchive(t, []*tar.Header{{Name: "root/environments/production.yaml", Typeflag: tar.TypeReg}}, []string{manifest + "environment: staging\n"}), 1 << 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := environmentgitops.ReadGitDefinition(bytes.NewReader(tc.bytes), "environments/production.yaml", tc.limit); err == nil {
				t.Fatal("unsafe source approved")
			}
		})
	}
}
