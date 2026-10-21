package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/onebox-faas/faas/pkg/api"
)

const deploySessionVersion = 1

// Save only recovery metadata and explicitly selected, non-secret settings.
// In particular, never save a deployment request, manifest or environment map.
type deploySessionRecord struct {
	Project                   *savedProjectDeploy                `json:"project,omitempty"`
	CronConfiguration         *api.DeploymentConfigurationStatus `json:"cron_configuration,omitempty"`
	UploadAcceptanceUncertain bool                               `json:"upload_acceptance_uncertain,omitempty"`
	CronJournal               *deployCronJournal                 `json:"cron_journal,omitempty"`
	PolicyJournal             *deployPolicyJournal               `json:"policy_journal,omitempty"`
	Version                   int                                `json:"version"`
	AttemptID                 string                             `json:"submission_attempt_id,omitempty"`
	ID                        string                             `json:"session_id"`
	Connection                string                             `json:"connection_sha256"`
	Intent                    string                             `json:"intent_sha256"`
	AppSlug                   string                             `json:"app_slug"`
	AppID                     string                             `json:"app_id,omitempty"`
	DeploymentID              string                             `json:"deployment_id,omitempty"`
	Outcome                   deploySubmissionOutcome            `json:"submission_outcome"`
	CreatedAt                 time.Time                          `json:"created_at"`
	WaitForRollout            bool                               `json:"wait_for_rollout,omitempty"`
	DarkDeploy                bool                               `json:"dark_deploy,omitempty"`
	SourceSHA256              string                             `json:"source_sha256,omitempty"`
	CommitSHA                 string                             `json:"commit_sha,omitempty"`
	Dirty                     bool                               `json:"dirty,omitempty"`
	SimpleAppPlan             *SimpleAppReceiptPlan              `json:"simple_app_plan,omitempty"`
	Upload                    *deploySessionUpload               `json:"upload,omitempty"`
	Settings                  []deploySettingsStep               `json:"settings,omitempty"`
}

type deploySessionUpload struct {
	ID        string `json:"id"`
	TotalSize int64  `json:"total_size"`
	ChunkSize int64  `json:"chunk_size"`
}

type savedDeploySession struct {
	record   deploySessionRecord
	path     string
	resuming bool
	key      string
}

type deploySessionContextKey struct{}

func sessionFromContext(ctx context.Context) *savedDeploySession {
	session, _ := ctx.Value(deploySessionContextKey{}).(*savedDeploySession)
	return session
}

func deployConnection(c *Client) string {
	data, _ := json.Marshal([]string{c.BaseURL(), currentProfile(), c.Token()})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func deploySessionDir(c *Client) (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		var err error
		base, err = os.UserConfigDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(base, "gregale", "deploys", deployConnection(c)), nil
}

func validDeploySessionID(id string) bool {
	decoded, err := hex.DecodeString(id)
	return err == nil && len(decoded) == 16 && strings.ToLower(id) == id
}

func newSavedDeploySession(ctx context.Context, c *Client, slug, appID, sourceDir, intent string, prov *zeroConfigProvenance, rollout, dark bool) (*savedDeploySession, *flock.Flock, error) {
	if _, err := validateConfigAPIBase(c.BaseURL()); err != nil {
		return nil, nil, err
	}
	steps, err := prepareDeploySettings(ctx, c, slug, sourceDir)
	if err != nil {
		return nil, nil, err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, nil, err
	}
	dir, err := deploySessionDir(c)
	if err != nil {
		return nil, nil, err
	}
	intentDigest := sha256.Sum256([]byte(intent))
	s := &savedDeploySession{key: intent, record: deploySessionRecord{
		Version: deploySessionVersion, ID: hex.EncodeToString(random[:]),
		Connection: deployConnection(c), Intent: hex.EncodeToString(intentDigest[:]),
		AppSlug: slug, AppID: appID, Outcome: deploySubmissionUnknown,
		CreatedAt: time.Now().UTC(), Settings: steps, WaitForRollout: rollout, DarkDeploy: dark,
	}}
	s.record.AttemptID = s.record.ID
	if prov != nil {
		s.record.CommitSHA, s.record.Dirty = prov.SHA, prov.Dirty
	}
	s.path = filepath.Join(dir, s.record.ID+".json")
	lock, err := lockDeploySession(s.path)
	if err != nil {
		return nil, nil, err
	}
	if err := s.save(); err != nil {
		_ = lock.Close()
		return nil, nil, err
	}
	return s, lock, nil
}

func lockDeploySession(path string) (*flock.Flock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	lock := flock.New(path+".lock", flock.SetPermissions(0o600))
	locked, err := lock.TryLock()
	if err != nil || !locked {
		_ = lock.Close()
		if err == nil {
			err = errors.New("another Gregale process is using this deploy session")
		}
		return nil, err
	}
	return lock, nil
}

func (s *savedDeploySession) save() error {
	data, err := json.MarshalIndent(s.record, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".deploy-*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), s.path); err != nil {
		return err
	}
	return api.SyncPrivateDeploymentSessionDirectory(dir)
}

func loadSavedDeploySession(c *Client, path string) (*savedDeploySession, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := &savedDeploySession{path: path, resuming: true}
	if err := json.Unmarshal(data, &s.record); err != nil {
		return nil, fmt.Errorf("decode deploy session: %w", err)
	}
	r := s.record
	slugValid := api.ValidAppSlug(r.AppSlug)
	if r.Project != nil {
		slugValid = api.ValidProjectSlug(r.AppSlug)
	}
	if r.Version != deploySessionVersion || !validDeploySessionID(r.ID) || filepath.Base(path) != r.ID+".json" || r.Connection != deployConnection(c) || !slugValid || r.CreatedAt.IsZero() {
		return nil, errors.New("deploy session has an unsupported format or a different connection")
	}
	if r.AttemptID != "" && !api.ValidDeploymentAttemptID(r.AttemptID) {
		return nil, errors.New("deploy session has an invalid submission attempt ID")
	}
	if r.Outcome != deploySubmissionAccepted && r.Outcome != deploySubmissionRejected && r.Outcome != deploySubmissionUnknown {
		return nil, errors.New("deploy session has an invalid submission outcome")
	}
	if r.Project != nil {
		if err := validateSavedProjectDeploy(r); err != nil {
			return nil, err
		}
		return s, nil
	}
	for _, step := range r.Settings {
		if err := validateDeploySettingsStep(step); err != nil {
			return nil, err
		}
	}
	if err := validateCronJournal(r); err != nil {
		return nil, err
	}
	if err := validatePolicyJournal(r); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *savedDeploySession) submitted(dep api.DeploymentResponse, err error) error {
	s.record.Outcome = submissionOutcome(dep, err)
	if dep.ID != "" && s.record.PolicyJournal != nil {
		s.record.PolicyJournal.Phase = deployPoliciesSubmissionStarted
	}
	if dep.ID != "" {
		s.record.DeploymentID = dep.ID
	}
	if dep.AppID != "" {
		if s.record.AppID != "" && s.record.AppID != dep.AppID {
			return errors.New("accepted deployment returned a different app identity")
		}
		s.record.AppID = dep.AppID
	}
	return s.save()
}

func saveSessionUpload(ctx context.Context, c *Client, state resumableUploadState) error {
	s := sessionFromContext(ctx)
	if s == nil {
		return nil
	}
	if state.CommitAttempted && s.record.PolicyJournal != nil {
		s.record.PolicyJournal.Phase = deployPoliciesSubmissionStarted
	}
	s.record.Upload = &deploySessionUpload{ID: state.UploadID, TotalSize: state.TotalSize, ChunkSize: state.ChunkSize}
	s.record.SourceSHA256 = state.ArchiveSHA256
	if j := s.record.CronJournal; j != nil && j.Managed && j.Deferred {
		s.record.UploadAcceptanceUncertain = true
	}
	if err := s.save(); err != nil {
		return err
	}
	if j := s.record.CronJournal; j != nil && j.Managed && j.Deferred {
		request := j.Intent
		request.Operation = "track_upload"
		request.UploadID = state.UploadID
		_, err := c.ApplyDeploymentCronStaging(ctx, s.record.AppSlug, request)
		return err
	}
	return nil
}

func (s *savedDeploySession) resumeCommand() string {
	return recoveryCLI() + " deploy resume " + s.record.ID
}

func (s *savedDeploySession) recovery(stage string) *deployRecovery {
	r := newDeployRecovery(stage, s.record.AppSlug, s.record.DeploymentID, s.key, 0, s.record.WaitForRollout)
	r.SessionID, r.SubmissionOutcome = s.record.ID, s.record.Outcome
	r.Configuration = s.record.CronConfiguration
	r.ResumeCommand = s.resumeCommand()
	r.Hint = "The server continues processing an accepted deployment. Resume this saved deploy to reconnect and complete its pending settings. Resume does not submit another deployment."
	if s.record.Outcome == deploySubmissionUnknown {
		r.Hint = "Acceptance remains uncertain. Resume checks the saved submission or upload receipt without submitting another build; inspect deployment history if acceptance cannot be confirmed."
	}
	if s.record.Outcome == deploySubmissionRejected {
		r.Hint = "Submission stopped before acceptance. Resume cannot submit a deployment; correct the error and rerun the original deploy."
	}
	if s.shouldCleanPolicies() {
		r.Hint = "No deployment needs to be submitted to recover this attempt. Resume the saved deploy to finish saved deployment settings cleanup; concurrent edits are preserved."
	}
	return r
}

func (s *savedDeploySession) failure(title, stage string, err error) int {
	return printErr(title, &deployRecoveryError{err: err, recovery: s.recovery(stage)})
}

func (s *savedDeploySession) decorate(receipt *DeployReceipt) {
	receipt.SessionID = s.record.ID
	if receipt.SimpleAppPlan == nil {
		receipt.SimpleAppPlan = s.record.SimpleAppPlan
	}
	receipt.ConfigurationComplete = new(bool)
	*receipt.ConfigurationComplete = true
	if s.record.CronConfiguration != nil {
		receipt.Configuration = s.record.CronConfiguration
	}
	if receipt.Configuration != nil {
		*receipt.ConfigurationComplete = receipt.Configuration.Complete
	}
	if j := s.record.CronJournal; j != nil && j.Managed && j.Deferred {
		receipt.CronActivation = j.Status
		if j.Status != "activated" {
			*receipt.ConfigurationComplete = false
		}
	}
	for _, step := range s.record.Settings {
		if !step.Done {
			*receipt.ConfigurationComplete = false
		}
	}
	receipt.ResumeCommand = s.resumeCommand()
	if receipt.Recovery != nil {
		receipt.Recovery.SessionID = s.record.ID
		if receipt.Recovery.ResumeCommand != "" {
			receipt.Recovery.ResumeCommand = receipt.ResumeCommand
		} else if receipt.WaitStage != "configuration" {
			receipt.ResumeCommand = ""
		}
	}
}

func sessionDeployReceipt(ctx context.Context, receipt *DeployReceipt) *DeployReceipt {
	if s := sessionFromContext(ctx); s != nil {
		s.decorate(receipt)
	}
	return receipt
}

func printSessionWaitRecovery(ctx context.Context, err error, dep api.DeploymentResponse, slug, stage string, timeout time.Duration, rollout bool, exit int) int {
	if s := sessionFromContext(ctx); s != nil {
		return printErr(fmt.Sprintf("Deployment %s stopped during %s", dep.ID, stage), &deployRecoveryError{err: err, recovery: s.recovery(stage), exit: exit})
	}
	return printDeploymentWaitRecovery(err, dep, slug, stage, timeout, rollout, exit)
}
