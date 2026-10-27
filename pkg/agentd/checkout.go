package agentd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/pkg/agentrunproto"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
	"github.com/onebox-faas/faas/pkg/storage"
)

// A run's checkout reaches its VM as an input drive (ADR-20261027090010 §3). agentd
// fetches the commit archive through githubd, which holds the installation
// tokens, and stores it as an input image keyed by the archive's digest. A
// small ref object maps (account, installation, repository, commit) to that
// digest, so later runs of the same commit skip GitHub, and each agent
// node's storage cache keeps the images it has used on local disk. No
// component outside the VM reads the archive's contents.

// SourceFetcher streams a commit archive; githubdgrpc.Client implements it.
type SourceFetcher interface {
	StreamSourceRef(ctx context.Context, accountID string, installationID int64, repoFullName, ref string, maxArchiveBytes int64) (*githubdgrpc.StreamSourceRefResult, error)
}

// Checkouts prepares input drives.
type Checkouts struct {
	Source  SourceFetcher
	Storage storage.StorageBackend
	// TempDir holds archives while they are fetched; empty: the OS default.
	TempDir string
}

// CheckoutError is a checkout that can never succeed for this run: the
// repository, commit or installation is wrong, or the archive is too large.
// Other errors are transient and requeue the run.
type CheckoutError struct {
	Code string
	Err  error
}

func (e *CheckoutError) Error() string { return "agentd: checkout: " + e.Err.Error() }
func (e *CheckoutError) Unwrap() error { return e.Err }

// Outcome codes of a failed checkout.
const (
	outcomeCheckoutFailed   = "checkout_failed"
	outcomeCheckoutTooLarge = "checkout_too_large"
)

// checkoutRef is what a ref object records.
type checkoutRef struct {
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

// Prepare returns the input drive's storage key and the request's checkout.
func (c *Checkouts) Prepare(ctx context.Context, claim agentstore.Claim) (string, agentrunproto.Checkout, error) {
	co := claim.Checkout
	plan := api.Plan(claim.Plan)
	if co == nil || !plan.Valid() {
		return "", agentrunproto.Checkout{}, errors.New("agentd: checkout: the run names none")
	}
	idx := plan.PlanIndex()
	limits := agentrunproto.Checkout{MaxExtractedBytes: api.AgentCheckoutExtractedMaxBytes[idx], MaxEntries: api.AgentCheckoutEntriesMax}
	refKey := checkoutRefKey(claim.AccountID, co)
	if ref, ok, err := c.readRef(ctx, refKey); err != nil {
		return "", agentrunproto.Checkout{}, err
	} else if ok {
		limits.SHA256, limits.Bytes = ref.SHA256, ref.Bytes
		return inputImageKey(claim.AccountID, ref.SHA256), limits, nil
	}
	ref, err := c.fetch(ctx, claim.AccountID, co, api.AgentCheckoutArchiveMaxBytes[idx])
	if err != nil {
		return "", agentrunproto.Checkout{}, err
	}
	body, err := json.Marshal(ref)
	if err != nil {
		return "", agentrunproto.Checkout{}, fmt.Errorf("agentd: checkout: encode ref: %w", err)
	}
	if err := c.Storage.Put(ctx, refKey, bytes.NewReader(body)); err != nil {
		return "", agentrunproto.Checkout{}, fmt.Errorf("agentd: checkout: store ref: %w", err)
	}
	limits.SHA256, limits.Bytes = ref.SHA256, ref.Bytes
	return inputImageKey(claim.AccountID, ref.SHA256), limits, nil
}

// readRef reads a ref object; ok is false when there is none.
func (c *Checkouts) readRef(ctx context.Context, key string) (checkoutRef, bool, error) {
	r, err := c.Storage.Get(ctx, key)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, storage.ErrNotFound) {
		return checkoutRef{}, false, nil
	}
	if err != nil {
		return checkoutRef{}, false, fmt.Errorf("agentd: checkout: read ref: %w", err)
	}
	defer func() { _ = r.Close() }()
	var ref checkoutRef
	if err := json.NewDecoder(io.LimitReader(r, 4096)).Decode(&ref); err != nil {
		return checkoutRef{}, false, fmt.Errorf("agentd: checkout: decode ref: %w", err)
	}
	if raw, err := hex.DecodeString(ref.SHA256); err != nil || len(raw) != sha256.Size || ref.Bytes < 1 {
		return checkoutRef{}, false, errors.New("agentd: checkout: ref is malformed")
	}
	return ref, true, nil
}

// fetch streams the archive into a temporary file, then stores its input
// image. The image is written before the ref, so a ref always names a
// stored image.
func (c *Checkouts) fetch(ctx context.Context, accountID string, co *agentstore.Checkout, maxBytes int64) (checkoutRef, error) {
	res, err := c.Source.StreamSourceRef(ctx, accountID, co.InstallationID, co.Repository, co.Commit, maxBytes)
	if err != nil {
		return checkoutRef{}, classifyFetch(err)
	}
	tmp, err := os.CreateTemp(c.TempDir, "agent-checkout-*.tar.gz")
	if err != nil {
		_ = res.Body.Close()
		return checkoutRef{}, fmt.Errorf("agentd: checkout: temp file: %w", err)
	}
	defer func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }()
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(tmp, h), res.Body)
	if err := errors.Join(copyErr, res.Body.Close()); err != nil {
		return checkoutRef{}, classifyFetch(err)
	}
	switch {
	case res.Stats.Truncated || n > maxBytes:
		return checkoutRef{}, &CheckoutError{Code: outcomeCheckoutTooLarge, Err: fmt.Errorf("the archive exceeds %d bytes", maxBytes)}
	case n == 0:
		return checkoutRef{}, &CheckoutError{Code: outcomeCheckoutFailed, Err: errors.New("the archive is empty")}
	case res.Stats.ResolvedCommitSHA != "" && res.Stats.ResolvedCommitSHA != co.Commit:
		return checkoutRef{}, &CheckoutError{Code: outcomeCheckoutFailed, Err: fmt.Errorf("githubd resolved commit %s, not %s", res.Stats.ResolvedCommitSHA, co.Commit)}
	}
	var sum [sha256.Size]byte
	copy(sum[:], h.Sum(nil))
	ref := checkoutRef{SHA256: hex.EncodeToString(sum[:]), Bytes: n}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return checkoutRef{}, fmt.Errorf("agentd: checkout: rewind archive: %w", err)
	}
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(agentrunproto.WriteInputImage(pw, tmp, n, sum)) }()
	if err := c.Storage.Put(ctx, inputImageKey(accountID, ref.SHA256), pr); err != nil {
		_ = pr.CloseWithError(err)
		return checkoutRef{}, fmt.Errorf("agentd: checkout: store input image: %w", err)
	}
	return ref, nil
}

// classifyFetch makes githubd's refusals final and leaves the rest
// transient.
func classifyFetch(err error) error {
	switch status.Code(err) {
	case codes.NotFound, codes.InvalidArgument, codes.PermissionDenied, codes.FailedPrecondition:
		return &CheckoutError{Code: outcomeCheckoutFailed, Err: err}
	}
	var problem *api.Problem
	if errors.As(err, &problem) && problem.Status >= 400 && problem.Status < 500 && problem.Status != 429 {
		return &CheckoutError{Code: outcomeCheckoutFailed, Err: err}
	}
	return fmt.Errorf("agentd: checkout: fetch: %w", err)
}

func checkoutRefKey(accountID string, co *agentstore.Checkout) string {
	repo := sha256.Sum256([]byte(co.Repository))
	return agentrunproto.InputKeyPrefix + "refs/" + accountID + "/" + strconv.FormatInt(co.InstallationID, 10) + "/" +
		hex.EncodeToString(repo[:]) + "/" + co.Commit + ".json"
}

func inputImageKey(accountID, digest string) string {
	return agentrunproto.InputImageKey(accountID, digest)
}
