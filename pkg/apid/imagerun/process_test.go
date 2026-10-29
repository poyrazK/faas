package imagerun_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type recordedCredential struct {
	job  state.Job
	cred api.PutJobRegistryCredentialRequest
}

// adr: 1790
func TestCreateRejectsInvalidProcessAndRegistryAuth(t *testing.T) {
	cases := map[string]struct {
		body     api.CreateImageRunRequest
		noSetter bool
		status   int
	}{
		"user with whitespace": {body: api.CreateImageRunRequest{Image: "alpine", User: "app user"}, status: http.StatusBadRequest},
		"user with two groups": {body: api.CreateImageRunRequest{Image: "alpine", User: "app:a:b"}, status: http.StatusBadRequest},
		"empty user name":      {body: api.CreateImageRunRequest{Image: "alpine", User: ":0"}, status: http.StatusBadRequest},
		"user too long":        {body: api.CreateImageRunRequest{Image: "alpine", User: strings.Repeat("u", 257)}, status: http.StatusBadRequest},
		"relative workdir":     {body: api.CreateImageRunRequest{Image: "alpine", Workdir: "work"}, status: http.StatusBadRequest},
		"workdir with NUL":     {body: api.CreateImageRunRequest{Image: "alpine", Workdir: "/a\x00b"}, status: http.StatusBadRequest},
		"workdir too long":     {body: api.CreateImageRunRequest{Image: "alpine", Workdir: "/" + strings.Repeat("d", 4096)}, status: http.StatusBadRequest},
		// Hunt 15: a guest this small cannot boot, so every attempt would fail.
		"ram below the job minimum": {body: api.CreateImageRunRequest{Image: "alpine", RAMMB: api.JobMinRAMMB - 1}, status: http.StatusBadRequest},
		"one megabyte of ram":       {body: api.CreateImageRunRequest{Image: "alpine", RAMMB: 1}, status: http.StatusBadRequest},
		"registry auth without password": {body: api.CreateImageRunRequest{Image: "ghcr.io/acme/tool:1",
			RegistryAuth: &api.ImageRunRegistryAuth{Username: "ci"}}, status: http.StatusBadRequest},
		"registry auth without username": {body: api.CreateImageRunRequest{Image: "ghcr.io/acme/tool:1",
			RegistryAuth: &api.ImageRunRegistryAuth{Password: "token"}}, status: http.StatusBadRequest},
		"registry auth unsupported by the store": {body: api.CreateImageRunRequest{Image: "ghcr.io/acme/tool:1",
			RegistryAuth: &api.ImageRunRegistryAuth{Username: "ci", Password: "token"}}, noSetter: true, status: http.StatusServiceUnavailable},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			hs := newHarness(t, api.PlanPro)
			if !tc.noSetter {
				hs.h.SetRegistryCredential = func(context.Context, state.Account, state.Job, api.PutJobRegistryCredentialRequest) *api.Problem {
					t.Fatal("credential stored for a rejected request")
					return nil
				}
			}
			status, _, body := hs.post(tc.body)
			if status != tc.status {
				t.Fatalf("status=%d body=%s; want %d", status, body, tc.status)
			}
			if strings.Contains(body, "token") {
				t.Fatalf("response echoed the registry password: %s", body)
			}
			if jobs, _ := hs.store.JobListByAccount(context.Background(), hs.acct.ID, 10, 0); len(jobs) != 0 || len(hs.notified) != 0 {
				t.Fatalf("rejected request touched state: jobs=%v notified=%v", jobs, hs.notified)
			}
		})
	}
}

// adr: 1790
func TestCreateSnapshotsProcessOverrides(t *testing.T) {
	hs := newHarness(t, api.PlanPro)
	req := api.CreateImageRunRequest{Image: "alpine", User: "1000:1000", Workdir: "/work", Args: strs("id")}
	status, resp, body := hs.post(req)
	if status != http.StatusAccepted {
		t.Fatalf("first post status=%d body=%s", status, body)
	}
	hs.materialize(resp.Job, nil, []string{"/bin/sh"})
	status, resp, body = hs.post(req)
	if status != http.StatusCreated {
		t.Fatalf("second post status=%d body=%s", status, body)
	}
	run, err := hs.store.JobRunGetByID(context.Background(), resp.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.ProcessUser != "1000:1000" || run.ProcessWorkdir != "/work" {
		t.Fatalf("run process = %q %q, want 1000:1000 /work", run.ProcessUser, run.ProcessWorkdir)
	}

	status, resp, body = hs.post(api.CreateImageRunRequest{Image: "alpine", Args: strs("id")})
	if status != http.StatusCreated {
		t.Fatalf("plain post status=%d body=%s", status, body)
	}
	if run, _ := hs.store.JobRunGetByID(context.Background(), resp.RunID); run.ProcessUser != "" || run.ProcessWorkdir != "" {
		t.Fatalf("plain run inherited overrides: %q %q", run.ProcessUser, run.ProcessWorkdir)
	}
}

// adr: 1790
func TestCreateStoresRegistryCredentialOnRunnerJobBeforePull(t *testing.T) {
	cases := map[string]struct {
		image, registry string
	}{
		"docker hub":       {image: "acme/private:1", registry: "registry-1.docker.io"},
		"ghcr":             {image: "ghcr.io/Acme/tool:1.4", registry: "ghcr.io"},
		"registry on port": {image: "registry.example:5000/team/app:2", registry: "registry.example:5000"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			hs := newHarness(t, api.PlanPro)
			var got []recordedCredential
			hs.h.SetRegistryCredential = func(_ context.Context, acct state.Account, job state.Job, cred api.PutJobRegistryCredentialRequest) *api.Problem {
				if acct.ID != hs.acct.ID {
					t.Fatalf("credential stored for account %s", acct.ID)
				}
				if len(hs.notified) != 0 {
					t.Fatalf("imaged was told to pull before the credential was stored: %v", hs.notified)
				}
				got = append(got, recordedCredential{job: job, cred: cred})
				return nil
			}
			status, resp, body := hs.post(api.CreateImageRunRequest{Image: tc.image,
				RegistryAuth: &api.ImageRunRegistryAuth{Username: "ci", Password: "s3cret-token"}})
			if status != http.StatusAccepted {
				t.Fatalf("status=%d body=%s", status, body)
			}
			if strings.Contains(body, "s3cret-token") {
				t.Fatalf("response echoed the password: %s", body)
			}
			if len(got) != 1 || got[0].job.Name != resp.Job || got[0].job.Kind != state.JobKindImageRun {
				t.Fatalf("credential calls = %+v; want one for runner job %s", got, resp.Job)
			}
			if got[0].cred != (api.PutJobRegistryCredentialRequest{Registry: tc.registry, Username: "ci", Password: "s3cret-token"}) {
				t.Fatalf("credential = %+v; want registry %s", got[0].cred, tc.registry)
			}
			if len(hs.notified) == 0 {
				t.Fatal("imaged was not told to pull after the credential was stored")
			}
		})
	}
}

// adr: 1790
func TestCreateReportsCredentialProblemWithoutAnnouncingPull(t *testing.T) {
	hs := newHarness(t, api.PlanPro)
	hs.h.SetRegistryCredential = func(context.Context, state.Account, state.Job, api.PutJobRegistryCredentialRequest) *api.Problem {
		return api.NewProblem(http.StatusForbidden, api.CodeValidation, "Quota", "registry credential quota reached")
	}
	status, _, body := hs.post(api.CreateImageRunRequest{Image: "ghcr.io/acme/tool:1",
		RegistryAuth: &api.ImageRunRegistryAuth{Username: "ci", Password: "token"}})
	if status != http.StatusForbidden || !strings.Contains(body, "quota reached") {
		t.Fatalf("status=%d body=%s; want the credential problem", status, body)
	}
	if len(hs.notified) != 0 {
		t.Fatalf("pull announced despite the credential failure: %v", hs.notified)
	}
}
