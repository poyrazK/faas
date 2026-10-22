package imagerun_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/imagerun"
	"github.com/onebox-faas/faas/pkg/state"
)

type harness struct {
	t        *testing.T
	store    *state.MemStore
	acct     state.Account
	h        *imagerun.Handler
	notified []string
}

func newHarness(t *testing.T, plan api.Plan) *harness {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(), "image-runs@example.com", plan)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	hs := &harness{t: t, store: store, acct: acct}
	hs.h = &imagerun.Handler{
		Enabled: true,
		Store:   store,
		Audit:   func(context.Context, string, *string, map[string]any) {},
		Notify: func(_ context.Context, _ string, payload string) error {
			hs.notified = append(hs.notified, payload)
			return nil
		},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return hs
}

func (hs *harness) post(body any) (int, api.ImageRunResponse, string) {
	hs.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		hs.t.Fatalf("marshal: %v", err)
	}
	rec := httptest.NewRecorder()
	hs.h.Create(rec, httptest.NewRequest(http.MethodPost, "/v1/image-runs", bytes.NewReader(raw)), hs.acct)
	var resp api.ImageRunResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return rec.Code, resp, rec.Body.String()
}

// materialize plays imaged for the runner job: claim, record the image
// config, publish.
func (hs *harness) materialize(jobName string, entrypoint, cmd []string) {
	hs.t.Helper()
	ctx := context.Background()
	job, err := hs.store.JobGetByName(ctx, hs.acct.ID, jobName)
	if err != nil {
		hs.t.Fatalf("JobGetByName: %v", err)
	}
	claimed, err := hs.store.JobClaimImageMaterialization(ctx, job.ID, "test", time.Minute)
	if err != nil {
		hs.t.Fatalf("claim: %v", err)
	}
	if err := hs.store.JobRecordImageConfig(ctx, job.ID, job.ImageRef, "test", claimed.ImageMaterializationAttempts, entrypoint, cmd); err != nil {
		hs.t.Fatalf("record config: %v", err)
	}
	if _, err := hs.store.JobPublishImageMaterialization(ctx, job.ID, job.ImageRef, "test", claimed.ImageMaterializationAttempts,
		"sha256:"+strings.Repeat("e", 64), "jobs/"+job.ID+"__test.ext4"); err != nil {
		hs.t.Fatalf("publish: %v", err)
	}
}

func strs(v ...string) *[]string { return &v }

// adr: 1161
func TestCreateRejectsBeforeTouchingState(t *testing.T) {
	cases := map[string]struct {
		plan    api.Plan
		disable bool
		body    api.CreateImageRunRequest
		status  int
		code    string
	}{
		"gate off":          {plan: api.PlanPro, disable: true, body: api.CreateImageRunRequest{Image: "alpine"}, status: http.StatusNotFound},
		"free plan":         {plan: api.PlanFree, body: api.CreateImageRunRequest{Image: "alpine"}, status: http.StatusPaymentRequired, code: api.CodeJobsNotAllowed},
		"missing image":     {plan: api.PlanPro, body: api.CreateImageRunRequest{}, status: http.StatusBadRequest, code: api.CodeValidation},
		"bad pull":          {plan: api.PlanPro, body: api.CreateImageRunRequest{Image: "alpine", Pull: "sometimes"}, status: http.StatusBadRequest, code: api.CodeValidation},
		"nul in args":       {plan: api.PlanPro, body: api.CreateImageRunRequest{Image: "alpine", Args: strs("a\x00b")}, status: http.StatusBadRequest, code: api.CodeValidation},
		"ram over plan cap": {plan: api.PlanHobby, body: api.CreateImageRunRequest{Image: "alpine", RAMMB: 1 << 20}, status: http.StatusForbidden, code: api.CodeJobQuotaExceeded},
		"reserved env key":  {plan: api.PlanPro, body: api.CreateImageRunRequest{Image: "alpine", Env: map[string]string{"GREGALE_X": "1"}}, status: http.StatusBadRequest, code: api.CodeValidation},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			hs := newHarness(t, tc.plan)
			hs.h.Enabled = !tc.disable
			status, _, body := hs.post(tc.body)
			if status != tc.status || (tc.code != "" && !strings.Contains(body, `"code":"`+tc.code+`"`)) {
				t.Fatalf("status=%d body=%s; want %d %s", status, body, tc.status, tc.code)
			}
			if jobs, _ := hs.store.JobListByAccount(context.Background(), hs.acct.ID, 10, 0); len(jobs) != 0 || len(hs.notified) != 0 {
				t.Fatalf("rejected request touched state: jobs=%v notified=%v", jobs, hs.notified)
			}
		})
	}
}

// adr: 1161
func TestCreatePullsThenQueuesDockerResolvedCommand(t *testing.T) {
	cases := map[string]struct {
		entrypoint, cmd []string
		req             api.CreateImageRunRequest
		want            []string
	}{
		"image defaults":          {entrypoint: []string{"/docker-entrypoint.sh"}, cmd: []string{"nginx", "-g", "daemon off;"}, want: []string{"/docker-entrypoint.sh", "nginx", "-g", "daemon off;"}},
		"args replace CMD":        {entrypoint: []string{"/bin/tool"}, cmd: []string{"--help"}, req: api.CreateImageRunRequest{Args: strs("version")}, want: []string{"/bin/tool", "version"}},
		"entrypoint drops CMD":    {entrypoint: []string{"/bin/tool"}, cmd: []string{"--help"}, req: api.CreateImageRunRequest{Entrypoint: strs("/bin/sh")}, want: []string{"/bin/sh"}},
		"entrypoint plus args":    {cmd: []string{"sh"}, req: api.CreateImageRunRequest{Entrypoint: strs("/bin/sh"), Args: strs("-c", "echo hi")}, want: []string{"/bin/sh", "-c", "echo hi"}},
		"CMD only image":          {cmd: []string{"echo", "hello"}, want: []string{"echo", "hello"}},
		"args only on bare image": {req: api.CreateImageRunRequest{Args: strs("echo", "hi")}, want: []string{"echo", "hi"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			hs := newHarness(t, api.PlanPro)
			tc.req.Image = "alpine"
			tc.req.Env = map[string]string{"GREETING": "hi"}
			status, pulling, body := hs.post(tc.req)
			if status != http.StatusAccepted || pulling.Status != api.ImageRunStatusPulling ||
				pulling.Image != "docker.io/library/alpine:latest" || !strings.HasPrefix(pulling.Job, "ir-") || len(hs.notified) != 1 {
				t.Fatalf("first call = %d %s notified=%v", status, body, hs.notified)
			}
			if count, _ := hs.store.JobCountByAccount(context.Background(), hs.acct.ID); count != 0 {
				t.Fatalf("runner job counted against the Job quota: %d", count)
			}
			hs.materialize(pulling.Job, tc.entrypoint, tc.cmd)
			status, queued, body := hs.post(tc.req)
			if status != http.StatusCreated || queued.Status != api.ImageRunStatusQueued || queued.RunID == "" ||
				queued.Job != pulling.Job || !reflect.DeepEqual(queued.Command, tc.want) {
				t.Fatalf("second call = %d %s; want command %q", status, body, tc.want)
			}
			run, err := hs.store.JobRunGetByID(context.Background(), queued.RunID)
			if err != nil {
				t.Fatalf("JobRunGet: %v", err)
			}
			if run.Tasks != 1 || run.RetryMax == nil || *run.RetryMax != 0 || !reflect.DeepEqual(run.Command, tc.want) ||
				!strings.Contains(string(run.EnvOverrides), `"GREETING":"hi"`) {
				t.Fatalf("run = %+v", run)
			}
		})
	}
}

// adr: 1161
func TestCreateRejectsImageWithoutCommand(t *testing.T) {
	hs := newHarness(t, api.PlanPro)
	_, pulling, _ := hs.post(api.CreateImageRunRequest{Image: "scratch-tool"})
	hs.materialize(pulling.Job, nil, nil)
	status, _, body := hs.post(api.CreateImageRunRequest{Image: "scratch-tool"})
	if status != http.StatusBadRequest || !strings.Contains(body, api.CodeImageRunCommandRequired) {
		t.Fatalf("status=%d body=%s", status, body)
	}
}

// adr: 1161
func TestCreateReportsFullCacheAndManagedNames(t *testing.T) {
	hs := newHarness(t, api.PlanHobby) // three cached images
	ctx := context.Background()
	for _, image := range []string{"busy-a", "busy-b", "busy-c"} {
		_, pulling, _ := hs.post(api.CreateImageRunRequest{Image: image, Args: strs("true")})
		hs.materialize(pulling.Job, nil, nil)
		if status, _, body := hs.post(api.CreateImageRunRequest{Image: image, Args: strs("true")}); status != http.StatusCreated {
			t.Fatalf("queue %s = %d %s", image, status, body)
		}
	}
	status, _, body := hs.post(api.CreateImageRunRequest{Image: "busy-d"})
	if status != http.StatusConflict || !strings.Contains(body, api.CodeImageRunCacheFull) || !strings.Contains(body, `"limit":3`) {
		t.Fatalf("full cache = %d %s", status, body)
	}
	name := imagerun.RunnerName("docker.io/library/taken:latest")
	if _, err := hs.store.JobCreate(ctx, hs.acct.ID, name, "batch", "taken", []string{"/bin/true"}, 128, 60, 1, 0, nil); err != nil {
		t.Fatalf("JobCreate: %v", err)
	}
	if status, _, body := hs.post(api.CreateImageRunRequest{Image: "taken"}); status != http.StatusConflict || !strings.Contains(body, api.CodeImageRunManaged) {
		t.Fatalf("name collision = %d %s", status, body)
	}
}

// postRaw returns the recorder so tests can read headers.
func (hs *harness) postRaw(body any) *httptest.ResponseRecorder {
	hs.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		hs.t.Fatalf("marshal: %v", err)
	}
	rec := httptest.NewRecorder()
	hs.h.Create(rec, httptest.NewRequest(http.MethodPost, "/v1/image-runs", bytes.NewReader(raw)), hs.acct)
	return rec
}

// adr: 1330
func TestCreateInteractiveRejectsBeforeTouchingState(t *testing.T) {
	cases := map[string]struct {
		disableInteractive bool
		body               api.CreateImageRunRequest
		status             int
		code               string
	}{
		"interactive gate off": {disableInteractive: true, body: api.CreateImageRunRequest{Image: "alpine", Interactive: true}, status: http.StatusNotImplemented, code: api.CodeNotImplemented},
		"tty gate off":         {disableInteractive: true, body: api.CreateImageRunRequest{Image: "alpine", Interactive: true, TTY: true}, status: http.StatusNotImplemented, code: api.CodeNotImplemented},
		"tty without -i":       {body: api.CreateImageRunRequest{Image: "alpine", TTY: true}, status: http.StatusBadRequest, code: api.CodeValidation},
		"tty without -i gated": {disableInteractive: true, body: api.CreateImageRunRequest{Image: "alpine", TTY: true}, status: http.StatusBadRequest, code: api.CodeValidation},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			hs := newHarness(t, api.PlanPro)
			hs.h.InteractiveEnabled = !tc.disableInteractive
			status, resp, body := hs.post(tc.body)
			if status != tc.status || !strings.Contains(body, `"code":"`+tc.code+`"`) || resp.Attach != nil {
				t.Fatalf("status=%d body=%s; want %d %s", status, body, tc.status, tc.code)
			}
			if jobs, _ := hs.store.JobListByAccount(context.Background(), hs.acct.ID, 10, 0); len(jobs) != 0 || len(hs.notified) != 0 {
				t.Fatalf("rejected request touched state: jobs=%v notified=%v", jobs, hs.notified)
			}
		})
	}
}

// adr: 1330
func TestCreateInteractiveReturnsOneTimeAttach(t *testing.T) {
	for name, tty := range map[string]bool{"pipes": false, "tty": true} {
		t.Run(name, func(t *testing.T) {
			hs := newHarness(t, api.PlanPro)
			hs.h.InteractiveEnabled = true
			req := api.CreateImageRunRequest{Image: "alpine", Interactive: true, TTY: tty, Args: strs("sh")}

			rec := hs.postRaw(req)
			var pulling api.ImageRunResponse
			_ = json.Unmarshal(rec.Body.Bytes(), &pulling)
			if rec.Code != http.StatusAccepted || pulling.Attach != nil || strings.Contains(rec.Body.String(), `"attach"`) ||
				rec.Header().Get("Cache-Control") == "no-store" {
				t.Fatalf("pulling response = %d %s headers=%v; want 202 without attach", rec.Code, rec.Body.String(), rec.Header())
			}
			hs.materialize(pulling.Job, nil, []string{"sh"})

			rec = hs.postRaw(req)
			var queued api.ImageRunResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &queued); err != nil || rec.Code != http.StatusCreated {
				t.Fatalf("queue = %d %s (%v)", rec.Code, rec.Body.String(), err)
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store", got)
			}
			attach := queued.Attach
			if attach == nil || len(attach.Token) < 40 || attach.Subprotocol != api.AppTaskAttachSubprotocol ||
				attach.Path != "/v1/jobs/"+queued.Job+"/runs/"+queued.RunID+"/tasks/0/attach" {
				t.Fatalf("attach = %+v (run %s job %s)", attach, queued.RunID, queued.Job)
			}
			session, err := hs.store.JobRunAttachSession(context.Background(), queued.RunID)
			if err != nil {
				t.Fatalf("JobRunAttachSession: %v", err)
			}
			digest := sha256.Sum256([]byte(attach.Token))
			if !bytes.Equal(session.TokenSHA256, digest[:]) || session.TTY != tty || session.AttachTimeoutSec != api.ImageRunAttachTimeoutSec {
				t.Fatalf("session = %+v; want digest of the token, tty=%v, window %d", session, tty, api.ImageRunAttachTimeoutSec)
			}
			run, err := hs.store.JobRunGetByID(context.Background(), queued.RunID)
			if err != nil {
				t.Fatalf("JobRunGet: %v", err)
			}
			if run.Tasks != 1 || run.RetryMax == nil || *run.RetryMax != 0 {
				t.Fatalf("run = %+v; want one task without retries", run)
			}

			// A second interactive run gets its own credential.
			rec = hs.postRaw(req)
			var again api.ImageRunResponse
			_ = json.Unmarshal(rec.Body.Bytes(), &again)
			if rec.Code != http.StatusCreated || again.Attach == nil || again.Attach.Token == attach.Token || again.RunID == queued.RunID {
				t.Fatalf("second run = %d %s; want a fresh run and credential", rec.Code, rec.Body.String())
			}
		})
	}
}

// adr: 1330
func TestCreateOrdinaryRunHasNoAttach(t *testing.T) {
	hs := newHarness(t, api.PlanPro)
	hs.h.InteractiveEnabled = true
	_, pulling, _ := hs.post(api.CreateImageRunRequest{Image: "alpine", Args: strs("true")})
	hs.materialize(pulling.Job, nil, nil)
	rec := hs.postRaw(api.CreateImageRunRequest{Image: "alpine", Args: strs("true")})
	var queued api.ImageRunResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &queued)
	if rec.Code != http.StatusCreated || queued.Attach != nil || rec.Header().Get("Cache-Control") == "no-store" {
		t.Fatalf("ordinary run = %d %s headers=%v", rec.Code, rec.Body.String(), rec.Header())
	}
	if _, err := hs.store.JobRunAttachSession(context.Background(), queued.RunID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("ordinary run has a session: %v", err)
	}
}
