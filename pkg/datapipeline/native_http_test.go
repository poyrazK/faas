package datapipeline

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type nativeDataAPI struct {
	request      func(method, subject, query, body string) (int, string)
	requestTable func(method, subject, table, query, body string) (int, string)
	close        func()
	fingerprint  string
}

func startNativeDataAPI(t *testing.T, ctx context.Context, targetURL, reader string) nativeDataAPI {
	t.Helper()
	if os.Getenv("POSTGREST_BINARY") == "" {
		t.Fatal("native acceptance requires the pinned PostgREST binary")
	}
	u, err := url.Parse(targetURL)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(reader, "pipeline-test")
	q := u.Query()
	q.Set("sslrootcert", os.Getenv("DATA_PIPELINE_TEST_CA"))
	u.RawQuery = q.Encode()
	directory, err := filepath.Abs("../../cmd/gregale/templates/data-api")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, "node", "test/pipeline-gateway.mjs")
	cmd.Dir = directory
	cmd.Env = append(os.Environ(), "DATABASE_URL="+u.String(), "DATA_API_SCHEMAS=public", "DATA_API_READ_ONLY=true", "DATA_API_ISSUER=https://pipeline-test.example", "DATA_API_JWKS_URL=https://pipeline-test.example/jwks", "DATA_API_AUDIENCE=pipeline-test")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	closeAPI := func() {
		once.Do(func() {
			_ = cmd.Process.Signal(syscall.SIGTERM)
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				_ = cmd.Process.Kill()
				<-done
			}
		})
	}
	t.Cleanup(closeAPI)
	scanner := bufio.NewScanner(io.LimitReader(stdout, 8192))
	if !scanner.Scan() {
		t.Fatal("native Data API fixture failed before its ready record")
	}
	var ready struct {
		Port        int    `json:"port"`
		Alice       string `json:"alice"`
		Bob         string `json:"bob"`
		Fingerprint string `json:"fingerprint"`
	}
	if err = json.Unmarshal(scanner.Bytes(), &ready); err != nil || ready.Port == 0 || ready.Alice == "" || ready.Bob == "" {
		t.Fatal("invalid native Data API ready record")
	}
	client := http.Client{Timeout: 3 * time.Second}
	requestTable := func(method, subject, table, query, body string) (int, string) {
		token := ready.Alice
		if subject == "bob" {
			token = ready.Bob
		}
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, "http://127.0.0.1:"+strconv.Itoa(ready.Port)+"/rest/v1/"+url.PathEscape(table)+"?"+query, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, err := io.ReadAll(io.LimitReader(res.Body, 4096))
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, string(data)
	}
	request := func(method, subject, query, body string) (int, string) {
		return requestTable(method, subject, "orders", query, body)
	}
	return nativeDataAPI{request: request, requestTable: requestTable, close: closeAPI, fingerprint: ready.Fingerprint}
}

func assertNativeDataAPI(t *testing.T, ctx context.Context, targetURL, reader string) {
	t.Helper()
	api := startNativeDataAPI(t, ctx, targetURL, reader)
	defer api.close()
	request := func(method, subject, body string) (int, string) {
		return api.request(method, subject, "id=eq.1&select=id,total", body)
	}
	status, body := request("GET", "alice", "")
	if status != 200 || !strings.Contains(body, "77.77") {
		t.Fatalf("signed user did not read replicated data: status=%d body=%s", status, body)
	}
	status, body = request("GET", "bob", "")
	if status != 200 || strings.TrimSpace(body) != "[]" {
		t.Fatalf("RLS leaked replicated rows: status=%d body=%s", status, body)
	}
	for _, method := range []string{"POST", "PATCH", "DELETE"} {
		status, _ = request(method, "alice", `{"id":1,"total":0}`)
		if status != 403 {
			t.Fatalf("mirror mutation %s returned %d", method, status)
		}
	}
}
