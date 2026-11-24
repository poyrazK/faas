package githubd

// ADR-20261122220000 amendment 1: a GitHub push applies the app's
// gregale.verify.yaml like `gregale deploy` does. The file is read from the
// app's own source root (never the repository root for an app in a
// subdirectory, so one app's checks never land on another), and stored
// before the build is enqueued so the new deployment is verified against it.
// A push can already deploy any code, so letting it set its own checks
// grants nothing a deploy did not.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/verifycontract"
)

// errPushContractInvalid marks a contract file the push cannot apply. It is
// permanent: retrying the delivery cannot fix the commit.
var errPushContractInvalid = errors.New("invalid verification contract")

// verificationContractPath is where an app's contract lives in the tree.
func verificationContractPath(rootDir string) string {
	root := strings.Trim(rootDir, "/")
	if root == "" || root == "." {
		return verifycontract.FileName
	}
	return path.Join(root, verifycontract.FileName)
}

// applyPushVerificationContract stores the app's contract from the pushed
// tree when it differs from the stored one. It reports whether it changed
// the stored contract. A missing file leaves the stored contract alone, as
// `gregale deploy` does; removing a contract is `gregale release check
// delete`.
func (s *Service) applyPushVerificationContract(ctx context.Context, fsys fs.FS, app state.App) (bool, error) {
	if s.Reconcile == nil {
		return false, nil
	}
	contracts, ok := s.Reconcile.Store.(state.VerificationContractStore)
	if !ok {
		return false, nil
	}
	file := verificationContractPath(app.RootDir)
	raw, err := fs.ReadFile(fsys, file)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", file, err)
	}
	contract, err := verifycontract.ParseYAML(raw)
	if err != nil {
		return false, fmt.Errorf("%w: %s: %w", errPushContractInvalid, file, err)
	}
	canonical, sum, err := contract.Canonical()
	if err != nil {
		return false, fmt.Errorf("%w: %s: %w", errPushContractInvalid, file, err)
	}
	stored, err := contracts.AppVerificationContract(ctx, app.ID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return false, fmt.Errorf("read stored verification contract: %w", err)
	}
	if err == nil && stored.SHA256 == sum {
		return false, nil
	}
	if _, err := contracts.PutAppVerificationContract(ctx, state.VerificationContract{
		AppID: app.ID, AccountID: app.AccountID, Contract: canonical, SHA256: sum, CheckCount: len(contract.Checks),
	}, nil); err != nil {
		return false, fmt.Errorf("store verification contract: %w", err)
	}
	return true, nil
}
