package builderd

// adr: 435. No export is approved by implicit platform-key membership.

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/buildpublisher"
	"github.com/onebox-faas/faas/pkg/state"
	"sort"
	"time"
)

func (b *Builderd) WithBuildPublisher(p buildpublisher.Signer) *Builderd {
	b.buildPublisher = p
	return b
}

func (b *Builderd) publishBuildExport(ctx context.Context, build state.Build, dep state.Deployment, app state.App, sourceHash string, result BuildResult) error {
	required := app.RequireSigned || app.SecurityPolicy.RequiresSignedImage()
	if b.buildPublisher == nil {
		if required {
			return fmt.Errorf("build publisher is not configured")
		}
		return nil
	}
	store, ok := b.store.(state.BuildExportPublicationStore)
	if !ok {
		return fmt.Errorf("build publication store unavailable")
	}
	digest, n, err := buildpublisher.MeasureExport(ctx, result.LayerPath)
	if err != nil {
		return err
	}
	if n != result.LayerBytes {
		return buildpublisher.ErrInvalid
	}
	claims := buildpublisher.Claims{Format: buildpublisher.Format, AccountID: canonicalBuildID(app.AccountID), OrgID: canonicalBuildID(app.OrgID), AppID: canonicalBuildID(app.ID), DeploymentID: canonicalBuildID(dep.ID), BuildID: canonicalBuildID(build.ID), ClaimStartedAt: build.StartedAt.UTC().Format(time.RFC3339Nano), SourceSHA256: sourceHash, ExportDigest: digest, ExportBytes: n, Runtime: app.Runtime, BuilderNodeID: b.builderNodeID}
	proof, err := b.buildPublisher.Sign(ctx, claims)
	if err != nil {
		return err
	}
	proof, err = b.approvedBuildPublisher(ctx, app, claims, proof)
	if err != nil {
		if !required && errors.Is(err, buildpublisher.ErrInvalid) {
			return nil
		}
		return err
	}
	in := state.BuildExportPublicationInput{ID: uuid.NewString(), Claims: claims, Proof: proof}
	return retryStateMutation(ctx, func() error { _, err := store.RecordBuildExportPublication(ctx, in); return err })
}

// Materialized standards give publishers generated app signer names. Select
// an already approved key by verified SPKI identity; the store rechecks it
// under its current control fence, so a list/read race cannot enroll a key.
func (b *Builderd) approvedBuildPublisher(ctx context.Context, app state.App, claims buildpublisher.Claims, proof buildpublisher.Proof) (buildpublisher.Proof, error) {
	signers, err := b.store.ListAppTrustedSigners(ctx, app.AccountID, app.ID)
	if err != nil {
		return buildpublisher.Proof{}, err
	}
	preferred := proof.PublisherName
	sort.Slice(signers, func(i, j int) bool {
		if signers[i].SignerName == preferred || signers[j].SignerName == preferred {
			return signers[i].SignerName == preferred && signers[j].SignerName != preferred
		}
		return signers[i].SignerName < signers[j].SignerName
	})
	for _, signer := range signers {
		candidate := proof
		candidate.PublisherName = signer.SignerName
		if buildpublisher.Verify(claims, candidate, signer.CosignPublicKey) == nil {
			return candidate, nil
		}
	}
	return buildpublisher.Proof{}, buildpublisher.ErrInvalid
}

func canonicalBuildID(s string) string {
	parsed, err := uuid.Parse(s)
	if err != nil {
		return s
	}
	return parsed.String()
}
