// adr: 375
package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

type ambiguousQualificationRestoreProvider struct {
	qualificationProvider
	restorePoint time.Time
	cleanup      DeleteRequest
}

func (p *ambiguousQualificationRestoreProvider) Restore(_ context.Context, request RestoreRequest) (ObservedDatabase, error) {
	p.restorePoint = request.PointInTime
	return ObservedDatabase{}, ErrUnavailable
}

func (p *ambiguousQualificationRestoreProvider) Delete(ctx context.Context, request DeleteRequest) (DeleteResult, error) {
	if request.RestoreSourceResourceID != "" {
		p.cleanup = request
		if request.RestorePointInTime.IsZero() || !request.RestorePointInTime.Equal(p.restorePoint) {
			return DeleteResult{}, ErrConflict
		}
	}
	return p.qualificationProvider.Delete(ctx, request)
}

func TestQualificationAmbiguousRestoreCleanupRetainsExactPoint(t *testing.T) {
	provider := &ambiguousQualificationRestoreProvider{qualificationProvider: qualificationProvider{capabilities: testCapabilities()}}
	_, err := QualifyProvider(t.Context(), provider, QualificationOptions{ProviderName: "fake", ResourceID: "ambiguous-restore", Spec: testSpec(), Mutating: true})
	if !errors.Is(err, ErrQualificationFailed) || provider.cleanup.ResourceID == "" || provider.cleanup.ProviderResourceID != "" ||
		provider.cleanup.RestoreSourceResourceID != provider.resourceID || provider.cleanup.RestorePointInTime.IsZero() || !provider.cleanup.RestorePointInTime.Equal(provider.restorePoint) {
		t.Fatalf("ambiguous restore cleanup lost source/point: %+v, %v", provider.cleanup, err)
	}
}
