package datapipelines

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidsource"
	"github.com/onebox-faas/faas/pkg/connectioncapacity"
	"github.com/onebox-faas/faas/pkg/datapipeline"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// DataAPISetup adapts saved pipeline intent to the existing app, managed
// binding and source-build owners. It never builds or executes customer code.
type DataAPISetup struct {
	Store    state.Store
	Bindings interface {
		Create(context.Context, managedpostgres.CreateBindingRequest) (managedpostgres.Binding, error)
		Get(context.Context, string, string) (managedpostgres.Binding, error)
		ListForApp(context.Context, string, string, string) ([]managedpostgres.AppBindingInventory, error)
	}
	Notifier     apidsource.Notifier
	Log          *slog.Logger
	Spool        string
	Origin       func(context.Context, state.App) string
	BuildApp     func(state.Account, string) (state.App, error)
	ConfigureApp func(context.Context, state.Account, state.App) error
	AdmitDeploy  func(context.Context, state.Account, state.App) error
	AppCreated   func(context.Context, state.App)
	// AppVisible reports whether the request's principal may still see an
	// app the account created (appaccess.VisibleApps). A setup names its
	// auth app in the request body, outside appaccess.Wrap, so a company
	// auth app stays unavailable to a creator who left the company.
	AppVisible func(context.Context, state.Account, state.App) bool
}

func (s *DataAPISetup) account(ctx context.Context, id string) (state.Account, error) {
	a, err := s.Store.AccountByID(ctx, id)
	if err != nil {
		return a, dataAPIError(err)
	}
	if !a.MayDeploy() || !a.EmailVerified() {
		return a, datapipeline.ErrUnavailable
	}
	return a, nil
}

func (s *DataAPISetup) auth(ctx context.Context, account, id string) (string, error) {
	app, err := s.Store.AppByID(ctx, id)
	if err != nil {
		return "", dataAPIError(err)
	}
	if app.AccountID != account || app.Status != state.AppActive || app.Type != state.AppTypeApp {
		return "", datapipeline.ErrNotFound
	}
	bindings, err := s.Bindings.ListForApp(ctx, account, app.ID, api.DefaultEnvScope)
	if err != nil {
		return "", dataAPIError(err)
	}
	valid := false
	for _, b := range bindings {
		if b.Access == string(managedpostgres.CredentialAuth) && b.State == string(managedpostgres.BindingStateReady) {
			valid = true
		}
	}
	d, err := s.Store.LiveDeploymentForScope(ctx, app.ID, api.DefaultEnvScope)
	if !valid || err != nil || d.Status != state.DeployLive {
		return "", datapipeline.ErrInvalid
	}
	origin := s.Origin(ctx, app)
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", datapipeline.ErrInvalid
	}
	return origin + "/auth/v1", nil
}

// authVisible answers a hidden auth app as absent when a setup is submitted.
// Saved setups keep running: the worker revalidates the app itself.
func (s *DataAPISetup) authVisible(ctx context.Context, account, id string) error {
	if s.AppVisible == nil {
		return nil
	}
	app, err := s.Store.AppByID(ctx, id)
	if err != nil {
		return dataAPIError(err)
	}
	if app.AccountID == account && !s.AppVisible(ctx, state.Account{ID: account}, app) {
		return datapipeline.ErrNotFound
	}
	return nil
}

func (s *DataAPISetup) Plan(ctx context.Context, account string, r datapipeline.SetupRequest) (datapipeline.DataAPIPlan, error) {
	if _, err := s.account(ctx, account); err != nil {
		return datapipeline.DataAPIPlan{}, err
	}
	if err := s.authVisible(ctx, account, r.AuthAppID); err != nil {
		return datapipeline.DataAPIPlan{}, err
	}
	issuer, err := s.auth(ctx, account, r.AuthAppID)
	if err != nil {
		return datapipeline.DataAPIPlan{}, err
	}
	if _, err = s.Store.AppBySlug(ctx, r.DataAPIName); !errors.Is(err, state.ErrNotFound) {
		if err == nil {
			err = state.ErrConflict
		}
		return datapipeline.DataAPIPlan{}, dataAPIError(err)
	}
	return datapipeline.DataAPIPlan{AppID: uuid.NewString(), AuthAppID: r.AuthAppID, Issuer: issuer, JWKSURL: issuer + "/.well-known/jwks.json", Audience: "authenticated"}, nil
}

func (s *DataAPISetup) ownedApp(ctx context.Context, job datapipeline.Setup) (state.Account, state.App, error) {
	a, err := s.account(ctx, job.AccountID)
	if err != nil {
		return a, state.App{}, err
	}
	issuer, err := s.auth(ctx, a.ID, job.DataAPIPlan.AuthAppID)
	if err != nil {
		return a, state.App{}, err
	}
	// A domain/Auth change needs a new reviewed setup, never a silent issuer switch.
	if issuer != job.DataAPIPlan.Issuer {
		return a, state.App{}, datapipeline.ErrConflict
	}
	app, err := s.Store.AppByID(ctx, job.DataAPIPlan.AppID)
	if err != nil {
		return a, app, dataAPIError(err)
	}
	if app.AccountID != a.ID || app.Slug != job.Request.DataAPIName || app.Type != state.AppTypeApp || app.Status != state.AppActive {
		return a, app, datapipeline.ErrConflict
	}
	return a, app, nil
}

func (s *DataAPISetup) Prepare(ctx context.Context, job datapipeline.Setup) (string, bool, error) {
	a, app, err := s.ownedApp(ctx, job)
	if errors.Is(err, datapipeline.ErrNotFound) {
		// Only create after verifying Auth independently; an absent/foreign Auth
		// app must never cause a new customer resource to be reserved.
		if _, authErr := s.auth(ctx, job.AccountID, job.DataAPIPlan.AuthAppID); authErr != nil {
			return "", false, authErr
		}
		app, err = s.BuildApp(a, job.Request.DataAPIName)
		if err != nil {
			return "", false, dataAPIError(err)
		}
		app.ID = job.DataAPIPlan.AppID
		app, err = s.Store.CreateAppIfUnderQuota(ctx, app, api.MustLimitsFor(a.Plan))
		if errors.Is(err, state.ErrConflict) {
			a, app, err = s.ownedApp(ctx, job)
		}
	}
	if err != nil {
		return "", false, dataAPIError(err)
	}
	if s.AppCreated != nil {
		s.AppCreated(ctx, app)
	}
	b, err := s.Bindings.Create(ctx, managedpostgres.CreateBindingRequest{AccountID: a.ID, DatabaseID: job.DatabaseID, AppID: app.ID, Scope: api.DefaultEnvScope, EnvironmentKey: "DATABASE_URL", Access: managedpostgres.CredentialReadOnly})
	if err != nil {
		return b.ID, false, dataAPIError(err)
	}
	if b.State != managedpostgres.BindingStateReady {
		return b.ID, false, nil
	}
	if err = s.ConfigureApp(ctx, a, app); err != nil {
		return b.ID, false, dataAPIError(err)
	}
	return b.ID, true, nil
}

func (s *DataAPISetup) Deploy(ctx context.Context, job datapipeline.Setup) (string, string, error) {
	a, app, err := s.ownedApp(ctx, job)
	if err != nil {
		return "", "", err
	}
	deployment, build := apidsource.IntentIDs(job.DataAPIPlan.IntentID, app.ID)
	accepted := false
	expectedSource := ""
	// Recover durable accepted work before charging deployment admission again.
	if d, readErr := s.Store.DeploymentByID(ctx, deployment); readErr == nil {
		if d.AppID != app.ID {
			return "", "", datapipeline.ErrConflict
		}
		accepted, expectedSource = true, d.SourceSHA256
		if d.Status == state.DeployFailed || d.Status == state.DeployCancelled || d.Status == state.DeploySuperseded {
			return deployment, build, nil
		}
		if b, buildErr := s.Store.BuildByID(ctx, build); buildErr == nil && b.DeploymentID == d.ID {
			return deployment, build, nil
		} else if buildErr != nil && !errors.Is(buildErr, state.ErrNotFound) {
			return "", "", dataAPIError(buildErr)
		}
	} else if !errors.Is(readErr, state.ErrNotFound) {
		return "", "", dataAPIError(readErr)
	}
	latest, latestErr := s.Store.LatestDeployment(ctx, app.ID)
	if latestErr == nil && latest.ID != deployment && latest.ID != job.DataAPIPlan.PreviousDeploymentID {
		return "", "", datapipeline.ErrConflict
	}
	if latestErr != nil && !errors.Is(latestErr, state.ErrNotFound) {
		return "", "", dataAPIError(latestErr)
	}
	b, err := s.Bindings.Get(ctx, a.ID, job.DataAPIPlan.BindingID)
	if err != nil {
		return "", "", dataAPIError(err)
	}
	if b.AppID != app.ID || b.DatabaseID != job.DatabaseID || b.Access != managedpostgres.CredentialReadOnly || b.State != managedpostgres.BindingStateReady {
		return "", "", datapipeline.ErrConflict
	}
	if !accepted {
		if err := s.AdmitDeploy(ctx, a, app); err != nil {
			return "", "", dataAPIError(err)
		}
	}
	source, size, err := s.source(job)
	if err != nil {
		return "", "", dataAPIError(err)
	}
	if expectedSource != "" {
		digest, digestErr := sourceDigest(source)
		if digestErr != nil {
			return "", "", datapipeline.ErrUnavailable
		}
		if fmt.Sprintf("%x", digest) != expectedSource {
			return "", "", datapipeline.ErrConflict
		}
	}
	settings, _ := json.Marshal(map[string]string{"DATA_API_SCHEMAS": "public", "DATA_API_ISSUER": job.DataAPIPlan.Issuer, "DATA_API_JWKS_URL": job.DataAPIPlan.JWKSURL, "DATA_API_AUDIENCE": job.DataAPIPlan.Audience, "DATA_API_READ_ONLY": "true"})
	health, _ := json.Marshal(api.DeploymentHealthcheck{Path: "/healthz"})
	reason := "Saved pipeline setup " + job.ID
	if job.DataAPIPlan.PreviousDeploymentID != "" {
		reason += "; retry of " + job.DataAPIPlan.PreviousDeploymentID
	}
	result, err := apidsource.Enqueue(ctx, s.Store, s.Notifier, apidsource.EnqueueParams{IntentID: job.DataAPIPlan.IntentID, AppID: app.ID, Kind: state.DeploymentKindDockerfile, SourcePath: source, SourceBytes: size, Scope: api.DefaultEnvScope, LogSpool: s.Spool, Log: s.Log, OverrideEnv: settings, OverrideHealthcheck: health, Reason: reason, ActorUserID: a.ID, ActorVia: "dashboard"})
	if err != nil {
		return "", "", dataAPIError(err)
	}
	return result.DeploymentID, result.BuildID, nil
}

func (s *DataAPISetup) State(ctx context.Context, job datapipeline.Setup) (string, string, error) {
	_, app, err := s.ownedApp(ctx, job)
	if err != nil {
		return "", "", err
	}
	d, err := s.Store.DeploymentByID(ctx, job.DataAPIPlan.DeploymentID)
	if err != nil {
		return "", "", dataAPIError(err)
	}
	if d.AppID != app.ID {
		return "", "", datapipeline.ErrConflict
	}
	if d.Status == state.DeploySuperseded {
		return "", "", datapipeline.ErrConflict
	}
	if d.Status != state.DeployLive {
		return string(d.Status), "", nil
	}
	latest, err := s.Store.LatestDeployment(ctx, app.ID)
	if err != nil {
		return "", "", dataAPIError(err)
	}
	if latest.ID != d.ID {
		return "", "", datapipeline.ErrConflict
	}
	b, err := s.Bindings.Get(ctx, job.AccountID, job.DataAPIPlan.BindingID)
	if err != nil {
		return "", "", dataAPIError(err)
	}
	if b.AppID != app.ID || b.DatabaseID != job.DatabaseID || b.Access != managedpostgres.CredentialReadOnly || b.State != managedpostgres.BindingStateReady {
		return "", "", datapipeline.ErrConflict
	}
	return "live", s.Origin(ctx, app), nil
}

func (s *DataAPISetup) source(job datapipeline.Setup) (string, int64, error) {
	root := filepath.Join(s.Spool, "pipeline-setups", job.ID)
	if err := os.MkdirAll(root, 0755); err != nil {
		return "", 0, err
	}
	// A unique staging file avoids concurrent lease holders truncating a
	// queued build's archive. Enqueue retains ownership of accepted sources.
	f, err := os.CreateTemp(root, "data-api-*.tar.gz")
	if err != nil {
		return "", 0, err
	}
	path := f.Name()
	_ = f.Close()
	defer func() { _ = os.Remove(path) }()
	if err = templates.TarGz("data-api", path); err != nil {
		_ = os.Remove(path)
		return "", 0, err
	}
	if err = os.Chmod(path, 0644); err != nil {
		return "", 0, err
	}
	canonical := filepath.Join(root, job.DataAPIPlan.IntentID+".tar.gz")
	if err = os.Link(path, canonical); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return "", 0, err
		}
		existing, existingErr := sourceDigest(canonical)
		candidate, candidateErr := sourceDigest(path)
		if existingErr != nil || candidateErr != nil || existing != candidate {
			return "", 0, datapipeline.ErrConflict
		}
	}
	// The standard builder resolves the template's synthetic wrapper using
	// tarball.ResolveSourceRoot, just as it does for CLI template uploads.
	info, err := os.Stat(canonical)
	if err != nil {
		return "", 0, err
	}
	return canonical, info.Size(), nil
}

func sourceDigest(path string) ([32]byte, error) {
	f, err := os.Open(path) //nolint:forbidigo // server-owned build spool path
	if err != nil {
		return [32]byte{}, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return [32]byte{}, err
	}
	return [32]byte(h.Sum(nil)), nil
}

func dataAPIError(err error) error {
	if err == nil {
		return nil
	}
	var quota *state.QuotaError
	switch {
	case errors.Is(err, connectioncapacity.ErrInsufficient), errors.Is(err, connectioncapacity.ErrUnknown):
		return err
	case errors.Is(err, datapipeline.ErrNotFound), errors.Is(err, datapipeline.ErrConflict), errors.Is(err, datapipeline.ErrInvalid), errors.Is(err, datapipeline.ErrUnavailable), errors.Is(err, datapipeline.ErrQuota):
		return err
	case errors.Is(err, state.ErrNotFound), errors.Is(err, managedpostgres.ErrNotFound):
		return datapipeline.ErrNotFound
	case errors.Is(err, state.ErrConflict), errors.Is(err, managedpostgres.ErrConflict):
		return datapipeline.ErrConflict
	case errors.As(err, &quota), errors.Is(err, managedpostgres.ErrQuotaExceeded):
		return datapipeline.ErrQuota
	case errors.Is(err, managedpostgres.ErrInvalid), errors.Is(err, managedpostgres.ErrUnsupported), errors.Is(err, managedpostgres.ErrNotInPlan):
		return datapipeline.ErrInvalid
	default:
		return datapipeline.ErrUnavailable
	}
}
