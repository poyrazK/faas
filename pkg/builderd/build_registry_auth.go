package builderd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// SealedRegistryCredential is one of the app's private-registry credentials
// (ADR-20261026091500). builderd never opens it: vmmd opens the password
// with the build seal domain into the builder VM's Docker config, so a
// Dockerfile can build FROM a private base image.
type SealedRegistryCredential struct {
	Registry       string
	Username       string
	PasswordSealed []byte
}

// loadBuildRegistryCredentials reads the app's registry credentials for a
// Dockerfile build. An IAM access key for an ECR registry is left out and
// its registry returned: pulls exchange such a key for ECR's password, which
// a builder cannot do, and the key itself must never reach a registry.
func loadBuildRegistryCredentials(ctx context.Context, store state.Store, app state.App) ([]SealedRegistryCredential, []string, error) {
	rows, err := store.ListAppRegistryCredentials(ctx, app.AccountID, app.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("load registry credentials: %w", err)
	}
	var creds []SealedRegistryCredential
	var skipped []string
	for _, row := range rows {
		if api.IsECRAccessKey(row.Registry, row.Username) {
			skipped = append(skipped, row.Registry)
			continue
		}
		creds = append(creds, SealedRegistryCredential{Registry: row.Registry, Username: row.Username, PasswordSealed: row.PasswordEncrypted})
	}
	return creds, skipped, nil
}

// emitRegistryCredentialLog names, in the build log, the registries the
// build pulls with credentials and the ECR access keys it cannot use.
// Usernames and passwords never appear.
func (b *Builderd) emitRegistryCredentialLog(ctx context.Context, buildID string, creds []SealedRegistryCredential, skippedECR []string) {
	if len(creds) > 0 {
		hosts := make([]string, len(creds))
		for i, cred := range creds {
			hosts[i] = cred.Registry
		}
		b.emitBuildLog(ctx, buildID, "pulling base images with the app's registry credentials for "+strings.Join(hosts, ", ")+"\n")
	}
	if len(skippedECR) > 0 {
		b.emitBuildLog(ctx, buildID, "ECR access keys are not used for Dockerfile base images ("+strings.Join(skippedECR, ", ")+"): store an ECR password with username AWS, or use a public base image\n")
	}
}

// registryCredentialsToProto hands the sealed credentials to vmmd in the
// builder's BuildSpec.
func registryCredentialsToProto(creds []SealedRegistryCredential) []*vmmdpb.BuildRegistryCredential {
	if len(creds) == 0 {
		return nil
	}
	out := make([]*vmmdpb.BuildRegistryCredential, len(creds))
	for i, cred := range creds {
		out[i] = &vmmdpb.BuildRegistryCredential{Registry: cred.Registry, Username: cred.Username, PasswordSealed: cred.PasswordSealed}
	}
	return out
}

// registryAuthScope keys a build that pulls with registry credentials to
// its app and the sealed credentials, as buildSecretScope does for
// secrets: an image built FROM a private base is reused only by the app
// whose credentials pulled it, and a replaced credential rebuilds. Empty
// without credentials, so every other recipe key is unchanged.
func registryAuthScope(appID string, creds []SealedRegistryCredential) string {
	if len(creds) == 0 {
		return ""
	}
	sorted := append([]SealedRegistryCredential(nil), creds...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Registry < sorted[j].Registry })
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s", appID)
	for _, cred := range sorted {
		sum := sha256.Sum256(cred.PasswordSealed)
		_, _ = fmt.Fprintf(h, "\x00%s\x00%s\x00%x", cred.Registry, cred.Username, sum)
	}
	return hex.EncodeToString(h.Sum(nil))
}
