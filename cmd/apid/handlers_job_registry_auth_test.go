package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestJobRegistryCredentialsRoundTrip(t *testing.T) {
	e := setupRegistry(t, api.PlanHobby)
	job := seedJob(t, e, "private-job", "registry.example/worker:latest")
	const marker = "job-password-marker"
	rec := e.do(t, http.MethodPut, "/v1/jobs/"+job+"/registry-credentials", api.PutJobRegistryCredentialRequest{
		Registry: "https://registry.example", Username: "robot", Password: marker,
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body.String())
	}
	putBody, _ := io.ReadAll(rec.Body)
	if bytes.Contains(putBody, []byte(marker)) {
		t.Fatalf("PUT response leaked password: %s", putBody)
	}

	rec = e.do(t, http.MethodGet, "/v1/jobs/"+job+"/registry-credentials", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET: %d %s", rec.Code, rec.Body.String())
	}
	body, _ := io.ReadAll(rec.Body)
	if bytes.Contains(body, []byte(marker)) {
		t.Fatalf("GET response leaked password: %s", body)
	}
	var list api.JobRegistryCredentialListResponse
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list.Count != 1 || list.QuotaMax != 2 || len(list.Credentials) != 1 {
		t.Fatalf("list = %+v, want one credential and Hobby quota 2", list)
	}
	if list.Credentials[0].Registry != "registry.example" || list.Credentials[0].Username != "robot" {
		t.Fatalf("credential = %+v", list.Credentials[0])
	}

	rec = e.do(t, http.MethodDelete, "/v1/jobs/"+job+"/registry-credentials?registry="+url.QueryEscape("https://registry.example"), nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE: %d %s", rec.Code, rec.Body.String())
	}
}

func TestJobRegistryCredentialsQuotaAndIsolation(t *testing.T) {
	e := setupRegistry(t, api.PlanHobby)
	job := seedJob(t, e, "quota-job", "registry.example/worker:latest")
	for _, host := range []string{"https://one.example", "https://two.example"} {
		rec := e.do(t, http.MethodPut, "/v1/jobs/"+job+"/registry-credentials", api.PutJobRegistryCredentialRequest{
			Registry: host, Username: "robot", Password: "secret",
		}, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT %s: %d %s", host, rec.Code, rec.Body.String())
		}
	}
	rec := e.do(t, http.MethodPut, "/v1/jobs/"+job+"/registry-credentials", api.PutJobRegistryCredentialRequest{
		Registry: "https://three.example", Username: "robot", Password: "secret",
	}, nil)
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), api.CodePlanJobRegistryCredentialQuota) {
		t.Fatalf("quota PUT: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, http.MethodDelete, "/v1/jobs/"+job+"/registry-credentials?registry="+url.QueryEscape("https://missing.example"), nil, nil); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), api.CodeJobRegistryCredentialNotFound) {
		t.Fatalf("missing DELETE: %d %s", rec.Code, rec.Body.String())
	}

	other := seedJob(t, e, "other-job", "registry.example/worker:latest")
	if rec := e.do(t, http.MethodGet, "/v1/jobs/"+other+"/registry-credentials", nil, nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"count":0`) {
		t.Fatalf("other job GET: %d %s", rec.Code, rec.Body.String())
	}
	storedJob, err := e.store.JobGetByName(context.Background(), e.acct.ID, job)
	if err != nil {
		t.Fatalf("JobGetByName: %v", err)
	}
	if _, err := e.store.GetJobRegistryCredential(context.Background(), e.acct.ID, storedJob.ID, "one.example"); err != nil {
		t.Fatalf("store credential after API PUT: %v", err)
	}
}
