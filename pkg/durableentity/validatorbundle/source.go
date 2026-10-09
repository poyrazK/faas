// adr: 858
package validatorbundle

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Source is an explicit secret-free source artifact, never extracted from an
// app rootfs. IDs and digest are assigned by the trusted build lifecycle.
type Source struct {
	Runtime    api.ExecutionRuntime `json:"runtime"`
	Entrypoint string               `json:"entrypoint"`
	Files      []api.ExecutionFile  `json:"files"`
}

func (a *Artifacts) Build(ctx context.Context, appID, deploymentID, archive, root string) error {
	if a == nil || !a.apps[appID] {
		return nil
	}
	if root != "" && (path.Clean(root) != root || strings.HasPrefix(root, "/") || root == ".." || strings.HasPrefix(root, "../") || strings.Contains(root, "\\")) {
		return ErrArtifactUnavailable
	}
	f, err := os.Open(archive)
	if err != nil {
		return ErrArtifactUnavailable
	}
	defer f.Close()
	zip, err := gzip.NewReader(f)
	if err != nil {
		return ErrArtifactUnavailable
	}
	defer zip.Close()
	limited := &io.LimitedReader{R: zip, N: api.MaxDurableEntityValidatorBuildArchiveBytes + 1}
	tr := tar.NewReader(limited)
	name := "gregale.validator.json"
	if root != "" && root != "." {
		name = root + "/" + name
	}
	var source Source
	found := false
	for entries := 0; ; entries++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ErrArtifactUnavailable
		}
		if entries >= api.SourceArchiveMaxEntries {
			return ErrArtifactUnavailable
		}
		if strings.TrimPrefix(header.Name, "./") != name {
			continue
		}
		if found || (header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA) || header.Size <= 0 || header.Size > api.MaxDurableEntityValidatorRegistryBytes {
			return ErrArtifactUnavailable
		}
		body, err := io.ReadAll(io.LimitReader(tr, api.MaxDurableEntityValidatorRegistryBytes+1))
		if err != nil || !strict(body, &source) {
			return ErrArtifactUnavailable
		}
		found = true
	}
	if !found || limited.N <= 0 {
		return ErrArtifactUnavailable
	}
	b := Bundle{AppID: appID, DeploymentID: deploymentID, Runtime: source.Runtime, Entrypoint: source.Entrypoint, Files: source.Files}
	b.SHA256 = Hash(b)
	return a.Publish(ctx, b)
}

// Transfer carries identical validator bytes to a copied deployment identity.
// It is only for artifact-preserving promotion/clone paths in the same app.
func (a *Artifacts) Transfer(ctx context.Context, appID, sourceDeployment, targetDeployment string) error {
	if a == nil || !a.apps[appID] {
		return nil
	}
	b, err := a.Resolve(ctx, appID, sourceDeployment)
	if err != nil {
		return err
	}
	if targetDeployment == "" {
		return nil
	}
	b.DeploymentID = targetDeployment
	return a.Publish(ctx, b)
}
