//go:build metal && object_storage_native

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/e2etest/storagefixture"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

type storageNativeCredential struct{ accessKey, secret string }

func (j *storageNativeJourney) bindingBase() string {
	return "/v1/apps/" + j.slug + "/buckets/" + j.bucket.ID
}

func gatewayPort(t *testing.T, endpoint *url.URL) int {
	t.Helper()
	port, err := strconv.Atoi(endpoint.Port())
	if err != nil || port < 1 || port > 65535 {
		t.Fatal("invalid owned storage gateway port")
	}
	return port
}

func storageNativeWait(t *testing.T, what string, ready func(context.Context) bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for !ready(ctx) {
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s", what)
		case <-ticker.C:
		}
	}
}

func (j *storageNativeJourney) apiGet(ctx context.Context, path string) ([]byte, int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, j.h.APIDURL+path, nil)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Authorization", "Bearer "+j.key)
	response, err := j.h.HTTPClient().Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	return data, response.StatusCode, err
}

func (j *storageNativeJourney) request(t *testing.T, method, path string, body []byte, want int, wake string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, j.h.EdgeURL()+"/"+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal("cannot create storage application request")
	}
	request.Host = j.slug + ".apps.test.example"
	request.Header.Set("Authorization", "Bearer "+j.token)
	request.Header.Set("Content-Type", "application/octet-stream")
	if path == "internal/after-restore" {
		request.Header.Set("X-Faas-After-Restore", "1")
		request.Header.Set("X-Forwarded-For", "127.0.0.1")
		request.Header.Set("X-Real-IP", "127.0.0.1")
	}
	response, err := j.h.HTTPClient().Do(request)
	if err != nil {
		t.Fatal("storage application request failed")
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, (10<<20)+1))
	if err != nil || response.StatusCode != want || len(data) > 10<<20 {
		t.Fatalf("storage application %s %s: status=%d want=%d", method, path, response.StatusCode, want)
	}
	if wake != "" {
		id := response.Header.Get("x-faas-wake-id")
		if id == "" {
			t.Fatal("storage application omitted wake receipt")
		}
		if _, err := e2etest.WaitForWakeMethod(ctx, t, j.pool, id, wake, 10*time.Second); err != nil {
			t.Fatalf("storage application did not use %s: %v", wake, err)
		}
	}
	return data
}

func (j *storageNativeJourney) write(t *testing.T, key string, body []byte) {
	t.Helper()
	if _, exists := j.expected[key]; exists {
		t.Fatal("native acceptance attempted to replay an object mutation")
	}
	j.request(t, http.MethodPut, "objects/"+key, body, http.StatusCreated, "")
	j.expected[key] = append([]byte(nil), body...)
}

func (j *storageNativeJourney) readAll(t *testing.T, wake string) {
	t.Helper()
	keys := make([]string, 0, len(j.expected))
	for key := range j.expected {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		t.Fatal("storage byte preservation requires an existing object")
	}
	for _, key := range keys {
		body := j.request(t, http.MethodGet, "objects/"+key, nil, http.StatusOK, wake)
		wake = ""
		if !bytes.Equal(body, j.expected[key]) {
			t.Fatal("storage recovery changed object bytes")
		}
	}
}

func (j *storageNativeJourney) credential(t *testing.T) storageNativeCredential {
	t.Helper()
	row, err := j.store.GetObjectS3Credential(t.Context(), j.account, j.bucket.ID, j.binding.ID)
	if err != nil {
		t.Fatal("independent storage credential observation failed")
	}
	namespace, value, err := secretbox.OpenBytes(j.identity, row.SecretSealed)
	if err != nil || namespace != s3gateway.CredentialSecretNamespace || len(value) != 40 {
		t.Fatal("independent storage credential decryption failed")
	}
	return storageNativeCredential{accessKey: row.AccessKeyID, secret: string(value)}
}

// signedRead is independent of guest helpers. A fresh SigV4 request distinguishes
// retired authority from a failed fixture; raw signing material never reaches logs.
func (j *storageNativeJourney) signedRead(t *testing.T, credential storageNativeCredential, key string, want int) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	request, err := storagefixture.SignedRead(ctx, j.gateway.URL, j.bucket.Name, key, aws.Credentials{AccessKeyID: credential.accessKey, SecretAccessKey: credential.secret})
	if err != nil {
		t.Fatal("independent storage request construction failed")
	}
	response, err := j.gateway.Client().Do(request)
	if err != nil {
		t.Fatal("independent storage request failed")
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil || response.StatusCode != want {
		t.Fatalf("independent signed storage read: status=%d want=%d", response.StatusCode, want)
	}
	return data
}

func (j *storageNativeJourney) lastListConnection(t *testing.T) uint64 {
	t.Helper()
	var connection uint64
	storageNativeWait(t, "validated storage transport observation", func(context.Context) bool {
		for _, observation := range j.gateway.Observations() {
			if !observation.Observer && observation.Listing && observation.Status == http.StatusOK && observation.AccessKey == j.binding.Credential.AccessKeyID {
				connection = observation.Connection
			}
		}
		return connection != 0
	})
	return connection
}

func (j *storageNativeJourney) secretVersions(t *testing.T) map[string]int64 {
	t.Helper()
	rows, err := j.store.ListAppSecretsInScope(t.Context(), j.account, j.appID, api.DefaultEnvScope)
	if err != nil || len(rows) != 7 {
		t.Fatal("storage runtime secret inventory incomplete")
	}
	versions := make(map[string]int64)
	for _, row := range rows {
		versions[row.Key] = row.DeliveryVersion
	}
	return versions
}

func (j *storageNativeJourney) waitForAdoption(t *testing.T) {
	t.Helper()
	storageNativeWait(t, "public storage application adoption", func(ctx context.Context) bool {
		data, status, err := j.apiGet(ctx, "/v1/apps/"+j.slug+"/bindings")
		var inventory api.AppBindingInventory
		if err != nil || status != http.StatusOK || json.Unmarshal(data, &inventory) != nil || !inventory.Complete {
			return false
		}
		for _, binding := range inventory.Bindings {
			if binding.Type != api.BindingTypeObjectStorage || binding.Binding != "GREGALE_S3_ASSETS" {
				continue
			}
			adoption := binding.ApplicationAdoption
			if adoption == nil || !adoption.Complete || adoption.Status != "current" || adoption.SecretsExpected != 6 ||
				adoption.SecretsObserved != 6 || adoption.Application.Current != 6 || len(adoption.Targets) != 6 {
				return false
			}
			for _, target := range adoption.Targets {
				if target.DeploymentID != j.deployment || target.WorkloadName != "" || target.ProcessGeneration == "" ||
					target.ProcessGeneration != target.ApplicationAckGeneration || target.ApplicationAckVersion != target.CurrentVersion {
					return false
				}
			}
			return true
		}
		return false
	})
}

func (j *storageNativeJourney) requireStale(t *testing.T, snapshot string) {
	t.Helper()
	var stale bool
	if err := j.pool.QueryRow(t.Context(), `SELECT stale FROM snapshots WHERE id=$1`, snapshot).Scan(&stale); err != nil || !stale {
		t.Fatal("retired storage snapshot remained eligible")
	}
}

func (j *storageNativeJourney) verifyProvider(t *testing.T) {
	t.Helper()
	bucket, err := j.store.GetObjectBucket(t.Context(), j.account, j.appID, j.bucket.ID)
	if err != nil {
		t.Fatal("independent provider inventory failed")
	}
	j.provider.mu.Lock()
	defer j.provider.mu.Unlock()
	objects := j.provider.buckets[bucket.PhysicalName]
	if len(objects) != len(j.expected) {
		t.Fatal("provider object inventory changed across guest lifecycle")
	}
	for key, expected := range j.expected {
		object, exists := objects[key]
		if !exists || !bytes.Equal(object.body, expected) || j.provider.putCalls[bucket.PhysicalName+"/"+key] != 1 {
			t.Fatal("provider object bytes changed or a mutation was replayed")
		}
	}
}
