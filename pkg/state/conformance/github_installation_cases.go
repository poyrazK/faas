package conformance

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func testGitHubInstallationOwnership(t *testing.T, fx *Fixture) {
	other, err := fx.Store.CreateAccount(fx.Ctx, "github-install-other@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	inst := state.GitHubInstall{
		AccountID: fx.Account.ID, InstallationID: 4242, AuditGithubLogin: "octocat",
		DefaultBranch: "main", SealedToken: []byte("first-sealed-token"), TokenExpiresAt: time.Now().Add(time.Hour),
	}
	if err := fx.Store.UpsertGitHubInstall(fx.Ctx, inst); err != nil {
		t.Fatal(err)
	}
	inst.SealedToken = []byte("refreshed-sealed-token")
	if err := fx.Store.UpsertGitHubInstall(fx.Ctx, inst); err != nil {
		t.Fatalf("same-account reconnect: %v", err)
	}
	claim := inst
	claim.AccountID, claim.SealedToken = other.ID, []byte("other-sealed-token")
	if err := fx.Store.UpsertGitHubInstall(fx.Ctx, claim); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("cross-account reconnect = %v, want ErrConflict", err)
	}
	got, err := fx.Store.GitHubInstallForAccountInstallation(fx.Ctx, fx.Account.ID, inst.InstallationID)
	if err != nil || got.AccountID != inst.AccountID || !bytes.Equal(got.SealedToken, inst.SealedToken) {
		t.Fatalf("original binding changed after rejected reconnect: %v", err)
	}
	if _, err := fx.Store.GitHubInstallForAccountInstallation(fx.Ctx, other.ID, inst.InstallationID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("rejected account can read installation: %v", err)
	}

	// Two simultaneous first connections must elect exactly one owner.
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, accountID := range []string{fx.Account.ID, other.ID} {
		concurrent := inst
		concurrent.AccountID, concurrent.InstallationID = accountID, 4243
		go func() {
			<-start
			results <- fx.Store.UpsertGitHubInstall(fx.Ctx, concurrent)
		}()
	}
	close(start)
	var successes, conflicts int
	for range 2 {
		switch err := <-results; {
		case err == nil:
			successes++
		case errors.Is(err, state.ErrConflict):
			conflicts++
		default:
			t.Fatalf("concurrent connection: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent owners: %d successes, %d conflicts", successes, conflicts)
	}
}
