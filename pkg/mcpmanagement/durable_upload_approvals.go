package mcpmanagement

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Use one journal record per staged directory so a different connection's
// plan snapshot cannot replace the source reservation. Previous bridges do
// not interpret a pending upload as an ordinary deploy plan on downgrade.
const uploadApprovalNamespace = "hosted-upload-approval/"

type storedUploadApproval struct {
	Namespace     string
	ReservedBytes int64
	Plan          releasePlan
	LatestID      string
	AppHash       [32]byte
	Review        *api.AppReleaseReview
	UploadID      string
	Approval      api.AgentApprovalPendingInfo
}

func (u *uploadStore) openDirectories() error {
	plans := filepath.Join(u.root, "plans")
	for _, dir := range []string{u.root, plans} {
		if err := prepareUploadDirectory(dir); err != nil {
			return fmt.Errorf("prepare private upload directory: %w", err)
		}
	}
	if err := u.restoreDirectories(plans); err != nil {
		return err
	}
	// Unused upload URLs were process-local. Remove their archives and any
	// abandoned source or checkpoint that has no matching journal record.
	if err := clearUploadDirectory(u.root, func(name string) bool { return name == "plans" }); err != nil {
		return err
	}
	return clearUploadDirectory(plans, func(name string) bool {
		dir := filepath.Join(plans, strings.TrimSuffix(name, ".cli-state"))
		_, keep := u.dirs[dir]
		return keep
	})
}

// Prepare operator-supplied directories before reading staged plans. Use a
// descriptor for chmod so it cannot follow a symlink substituted for the path.
func prepareUploadDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("upload path must be a directory")
	}
	if info.Mode().Perm() == 0o700 {
		return nil
	}
	dir, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_DIRECTORY, 0) //nolint:forbidigo // operator-configured directory, opened without following links
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Chmod(0o700)
}

func clearUploadDirectory(dir string, keep func(string) bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read upload directory: %w", err)
	}
	for _, e := range entries {
		if !keep(e.Name()) {
			if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
				return fmt.Errorf("clear abandoned upload: %w", err)
			}
		}
	}
	return nil
}

func privateUploadDirectory(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && ownerOnly(info.Mode().Perm())
}

func (s *StateStore) uploadApprovalRecords() (map[string]json.RawMessage, error) {
	if s == nil {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil || s.closed {
		return nil, errors.New("MCP state unavailable")
	}
	records := map[string]json.RawMessage{}
	for name, data := range s.records {
		if strings.HasPrefix(name, uploadApprovalNamespace) {
			records[name] = data
		}
	}
	return records, nil
}

func (u *uploadStore) restoreDirectories(plans string) error {
	records, err := u.state.uploadApprovalRecords()
	if err != nil {
		return err
	}
	now := time.Now()
	discard := map[string]any{}
	for name, data := range records {
		id := strings.TrimPrefix(name, uploadApprovalNamespace)
		var record storedUploadApproval
		if !validUploadHex(id, 16) || json.Unmarshal(data, &record) != nil || !record.valid(now) {
			discard[name] = nil
			continue
		}
		dir := filepath.Join(plans, id)
		if !privateUploadDirectory(dir) {
			discard[name] = nil
			continue
		}
		u.dirs[dir] = planDir{accountID: record.Plan.AccountID, expires: record.Plan.ExpiresAt, bytes: record.ReservedBytes, approval: &record}
	}
	if len(discard) > 0 {
		return u.state.updateMany(discard)
	}
	return nil
}

func validUploadHex(value string, size int) bool {
	if len(value) != 2*size {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func (r storedUploadApproval) valid(now time.Time) bool {
	p := r.Plan
	source := p.LocalSource
	if r.Namespace == "" || !validUploadHex(p.ID, 32) || p.AccountID == "" || p.APIBase == "" || p.AppID == "" ||
		!api.ValidAppSlug(p.App) || p.Scope != api.DefaultEnvScope ||
		(p.Action != "local_deploy" && (p.Action != "deploy" || r.Review == nil)) ||
		source == nil || !source.Uploaded || source.CreateOnly || source.Path != "" ||
		!validUploadHex(source.Fingerprint, 32) || r.ReservedBytes <= 0 || r.ReservedBytes > maxStagedBytesPerAccount {
		return false
	}
	deadline, err := time.Parse(time.RFC3339, r.Approval.ExpiresAt)
	return err == nil && deadline.Equal(p.ExpiresAt) && now.Before(deadline) && !deadline.After(now.Add(api.AgentApprovalTTL)) &&
		validateSegment(r.Approval.ID, "approval_id") == nil && validateSegment(r.UploadID, "upload_id") == nil
}

// retainApproval commits the plan and its reservation before apply returns
// the review link. The caller holds the staged source pinned.
func (u *uploadStore) retainApproval(p *releasePlan, namespace string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	dir := p.LocalSource.Path
	d, ok := u.dirs[dir]
	if !ok || d.inUse == 0 {
		return invalid("plan_expired", "the reviewed upload is no longer staged")
	}
	if u.state != nil && namespace != "" {
		plan, source := *p, *p.LocalSource
		source.Path = ""
		plan.LocalSource = &source
		record := &storedUploadApproval{
			Namespace: namespace, ReservedBytes: d.bytes, Plan: plan, LatestID: p.latestID, AppHash: p.appHash,
			Review: p.review, UploadID: p.uploadApproval.uploadID, Approval: p.uploadApproval.info,
		}
		if d.accountID != p.AccountID || !record.valid(time.Now()) {
			return invalid("plan_stale", "the staged upload and approval could not be retained")
		}
		if err := u.state.update(uploadApprovalNamespace+filepath.Base(dir), record); err != nil {
			return err
		}
		d.approval = record
		d.expires = p.ExpiresAt
	} else if d.expires.Before(p.ExpiresAt) {
		d.expires = p.ExpiresAt
	}
	u.dirs[dir] = d
	return nil
}

// Only the grant or credential that reviewed the upload can see the plan.
// Native approval, app and source checks still run on every approved retry.
func (m *management) restoreUploadApprovals() error {
	u := m.opts.uploads
	if u == nil || m.opts.planNamespace == "" || !m.opts.AllowWrites || m.defaultAccess(true) != nil {
		return nil
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	now := time.Now()
	for dir, d := range u.dirs {
		r := d.approval
		if r == nil || r.Namespace != m.opts.planNamespace || !now.Before(d.expires) {
			continue
		}
		p := r.Plan
		if m.plans[p.ID] != nil || p.APIBase != m.opts.APIBase || p.AccountID != m.opts.accountID ||
			(m.opts.AllowedApps != nil && m.opts.AllowedApps[p.App] != p.AppID) {
			continue
		}
		if len(m.plans)+len(m.environmentPlans) >= maxReleasePlans {
			return errors.New("invalid stored upload plan capacity")
		}
		source := *p.LocalSource
		source.Path = dir
		p.LocalSource = &source
		p.appHash, p.latestID, p.review, p.attempted = r.AppHash, r.LatestID, r.Review, true
		p.uploadApproval = &pendingUploadApproval{uploadID: r.UploadID, info: r.Approval}
		m.plans[p.ID] = &p
	}
	return nil
}
