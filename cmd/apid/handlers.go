package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) whoami(w http.ResponseWriter, r *http.Request, acct state.Account) {
	writeJSON(w, http.StatusOK, s.accountResponse(ctx(r), acct, r))
}

func (s *server) listApps(w http.ResponseWriter, r *http.Request, acct state.Account) {
	apps, err := s.store.ListApps(ctx(r), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list apps"))
		return
	}
	out := make([]api.AppResponse, 0, len(apps))
	for _, a := range apps {
		out = append(out, s.appResponse(a))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) createApp(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.CreateAppRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	app, prob := s.buildApp(acct, req, limits)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	// Deployed-app count quota + insert happen in the same critical
	// section inside the store (PgStore: SELECT … FOR UPDATE on the
	// parent accounts row; MemStore: m.mu). This closes the TOCTOU the
	// previous CountDeployedApps + CreateApp pair exposed on Free/Hobby
	// accounts under concurrency (spec §4.2).
	created, err := s.store.CreateAppIfUnderQuota(ctx(r), app, limits)
	if err != nil {
		var qe *state.QuotaError
		switch {
		case errors.As(err, &qe):
			api.WriteProblem(w, api.ErrPlanLimitApps(limits, qe.Observed))
		case errors.Is(err, state.ErrConflict):
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
				"Slug taken", fmt.Sprintf("app slug %q is already in use", req.Slug)))
		default:
			api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeCapacity,
				"Capacity", "could not create app"))
		}
		return
	}
	// CodeQL go/log-injection (CWE-117): created.Slug passes validSlug's
	// regex check before persist (^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$),
	// but CodeQL's taint engine doesn't model that sanitizer. Wrap the
	// slug in logsanitize.Field so the audit line is one-event-per-line
	// regardless of what a future refactor of validSlug does.
	s.log.Info("app created", "app", created.ID, "slug", logsanitize.Field(created.Slug), "account", acct.ID)
	writeJSON(w, http.StatusCreated, s.appResponse(created))
}

// buildApp applies defaults and validates a create request, returning the App to
// persist or a *Problem describing the first violation.
func (s *server) buildApp(acct state.Account, req api.CreateAppRequest, limits api.Limits) (state.App, *api.Problem) {
	if !validSlug(req.Slug) {
		return state.App{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid slug", "slug must be 3–40 chars, lowercase letters, digits, and hyphens")
	}
	typ := state.AppType(orDefault(req.Type, string(state.AppTypeApp)))
	if typ != state.AppTypeApp && typ != state.AppTypeFunction {
		return state.App{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid type", "type must be app or function")
	}
	if typ == state.AppTypeFunction && req.Runtime != "node22" && req.Runtime != "python312" {
		return state.App{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid runtime", "functions require runtime node22 or python312")
	}
	ram := req.RAMMB
	if ram == 0 {
		ram = limits.RAMMB
	}
	mc := req.MaxConcurrency
	if mc == 0 {
		mc = 1
	}
	if prob := api.ValidateAppConfig(limits, ram, mc); prob != nil {
		return state.App{}, prob
	}
	return state.App{
		AccountID: acct.ID, Slug: req.Slug, Type: typ, Runtime: req.Runtime,
		RAMMB: ram, MaxConcurrency: mc, IdleTimeoutS: req.IdleTimeoutS, Status: state.AppActive,
	}, nil
}

func (s *server) createDeployment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	// DeployedApps (the per-account cap on apps) is enforced at app-create
	// time via store.CreateAppIfUnderQuota — the deploy path cannot
	// bypass it because the parent apps row must already exist. The
	// active-app gate that prevents an orphan deployment row pointing
	// at a soft-deleted app lives inside store.CreateDeployment
	// (PR-A: SELECT 1 FROM apps WHERE id=$1 AND status='active' FOR UPDATE).
	// If the app was deleted between this AppBySlug and the
	// CreateDeployment INSERT, the store returns ErrNotFound and we
	// surface the same 404 as a missing slug.
	app, err := s.store.AppBySlug(ctx(r), r.PathValue("slug"))
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such app")
		return
	}
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		// Cap upload size at the plan's SourceTarballMaxMB before any
		// multipart parsing — MaxBytesReader returns a *MaxBytesError on
		// overflow, which r.MultipartReader surfaces as a parse error that
		// createDeploymentMultipart already maps to 413. The pre-Check
		// in deploy_inputs.go only fires when ContentLength is known.
		limits := api.MustLimitsFor(acct.Plan)
		max := int64(limits.SourceTarballMaxMB) * 1024 * 1024
		r.Body = http.MaxBytesReader(w, r.Body, max)
		s.createDeploymentMultipart(w, r, acct, app)
		return
	}
	var req api.CreateDeploymentRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if !isDigestPinned(req.Image) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeImageRequired,
			"Image required", "image: deploys require a digest-pinned reference, e.g. registry.DOMAIN/app@sha256:..."))
		return
	}
	// PR-B: the prior-deployment supersede is folded into
	// store.CreateDeployment's tx (pkg/state/pgstore.go). apid no longer
	// holds a "supersede then create" two-step — the in-tx ordering
	// guarantees the previous live deployment is NEVER observed
	// superseded without the new pending row also being visible. We
	// read the prior row BEFORE the call via LatestDeployment so the
	// supersede-notify can carry its id; this keeps the return shape
	// 2-tuple and backward-compatible with pre-PR-B call sites.
	prev, _ := s.store.LatestDeployment(ctx(r), app.ID)
	d, err := s.store.CreateDeployment(ctx(r), state.Deployment{
		AppID: app.ID, ImageDigest: req.Image, Kind: state.DeploymentKindImage, Status: state.DeployPending,
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not create deployment"))
		return
	}
	// F-03: deployment_changed emits now carry status + deployment_id.
	// status="pending" tells listeners this row is still in-flight (builderd
	// will eventually stamp rootfs_path → imaged converts to ext4); later
	// transitions re-emit with status="live"|"failed"|"superseded".
	// deployment_id==to here, but imaged switches on deployment_id in
	// handleDeployment. Apid does not synthesise every transition — the
	// state machine walks pending→building→imaging→snapshotting→live and
	// each row write is followed by a NotifyDeploymentChanged. The image
	// branch below covers the first hop (submitted); later hops land in
	// cmd/apid/deploy_steps.go.
	_ = s.notif.Notify(ctx(r), db.NotifyDeploymentChanged,
		fmt.Sprintf(`{"kind":"image","status":"pending","app_id":"%s","deployment_id":"%s","to":"%s"}`, app.ID, d.ID, d.ID))
	// PR-B: if a prior row was just superseded inside the same tx,
	// fire a second NotifyDeploymentChanged so imaged's F5 cleanup
	// handler (handleDeploymentChanged) can drop the prior snapshot.
	// The notify carries status="superseded" + to=prev.ID; if no prev
	// existed (first deploy on this app), skip the second notify.
	if prev.ID != "" {
		_ = s.notif.Notify(ctx(r), db.NotifyDeploymentChanged,
			fmt.Sprintf(`{"kind":"image","status":"superseded","app_id":"%s","deployment_id":"%s","to":"%s"}`, app.ID, prev.ID, prev.ID))
	}
	// Sanitize req.Image at the log sink — CodeQL go/log-injection (CWE-117).
	// isDigestPinned already rejects malformed refs with 400 before this line,
	// but a future field/wrapper change would break that invariant. Sanitizing
	// here means the log statement stays safe regardless of upstream changes.
	// d.ID and app.ID are server-generated UUIDs — no sanitize needed.
	s.log.Info("deployment created", "deployment", d.ID, "app", app.ID, "ref", logsanitize.Field(req.Image))
	writeJSON(w, http.StatusAccepted, s.deploymentResponse(d))
}

func (s *server) appResponse(a state.App) api.AppResponse {
	return api.AppResponse{
		ID: a.ID, Slug: a.Slug, Type: string(a.Type), Runtime: a.Runtime,
		RAMMB: a.RAMMB, MaxConcurrency: a.MaxConcurrency, IdleTimeoutS: a.IdleTimeoutS,
		// ux_spec §6.5: per-app floor the reaper honors when
		// parking idle instances. Pro/Scale only (apid gates).
		MinInstances: a.MinInstances,
		Status:       string(a.Status), URL: fmt.Sprintf("https://%s.apps.%s", a.Slug, s.domain),
		Manifest: api.AppManifest{
			Entrypoint: a.Manifest.Entrypoint,
			Env:        a.Manifest.Env,
			WorkingDir: a.Manifest.WorkingDir,
			Port:       a.Manifest.Port,
			Healthz:    a.Manifest.Healthz,
			User:       a.Manifest.User,
		},
	}
}

// accountResponse builds the AccountResponse DTO, populating Limits
// (plan caps), AppCount (deployed apps), and UsageGBHours (current
// calendar month). Errors from store reads are swallowed — best
// effort; the dashboard renders the row even when the meter is
// temporarily unavailable (meterd republishes every minute).
//
// GitHubInstall is left empty for now; slice 8 fills it from
// githubd's bindings table once the daemon is live.
func (s *server) accountResponse(ctx context.Context, acct state.Account, r *http.Request) api.AccountResponse {
	l := api.MustLimitsFor(acct.Plan)
	resp := api.AccountResponse{
		ID:     acct.ID,
		Email:  acct.Email,
		Plan:   string(acct.Plan),
		Status: string(acct.Status),
		Limits: api.AccountLimits{
			Plan:            string(acct.Plan),
			RAMMB:           l.RAMMB,
			MaxConcurrency:  l.MaxConcurrency,
			DeployedApps:    l.DeployedApps,
			IncludedGBHours: int64(l.IncludedGBHours),
			AppLayerMaxMB:   l.AppLayerMaxMB,
		},
	}
	if r != nil {
		if n, err := s.store.CountDeployedApps(ctx, acct.ID); err == nil {
			resp.AppCount = n
		}
		month := time.Now().UTC()
		if rows, err := s.store.UsageByMonth(ctx, acct.ID, month); err == nil {
			var mbSec int64
			for _, u := range rows {
				mbSec += u.MBSeconds
			}
			resp.UsageGBHours = float64(mbSec) / 3_600_000.0
		}
	}
	return resp
}

var slugRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$`)

func validSlug(s string) bool { return slugRe.MatchString(s) }

// digestPinnedRE matches a digest-pinned OCI reference end-to-end:
//
//	<host>[/<repo-path>]/<name>@sha256:<64 lowercase hex>
//
// Where:
//
//	host     = RFC 1123 hostname (alnum + '-', dot-separated labels,
//	           optional :<port>)
//	repo     = alnum + '_-' + '.' + '/' (the OCI repository path grammar)
//
// The whole-ref anchoring is load-bearing: parseImageDigest feeds
// apid.createDeployment's slog log of req.Image (CodeQL go/log-injection),
// so a substring-search validator that only verifies the digest tail would
// let any non-OCI prefix through (including control chars / whitespace /
// extra @-separators). The host charset forbids control chars and
// whitespace explicitly, so the entire accepted string is printable OCI.
var digestPinnedRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)*(:[0-9]+)?/[A-Za-z0-9_./-]+@sha256:[0-9a-f]{64}$`)

// parseImageDigest requires a digest-pinned reference (spec gap G1: public
// registries, digest-pinned) and returns the digest portion (sha256:...).
func parseImageDigest(ref string) (string, bool) {
	if !digestPinnedRE.MatchString(ref) {
		return "", false
	}
	return ref[strings.Index(ref, "@"):], true
}

// isDigestPinned reports whether ref is a digest-pinned reference (the form
// the deploy contract requires). Use this for input validation; consumers
// parse the full ref via oci.ParseReference so they can dial the right
// registry host.
func isDigestPinned(ref string) bool {
	return digestPinnedRE.MatchString(ref)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
