// profilefixtures provisions disposable deployments for native profiling CI.
package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type journal struct {
	Slug          string   `json:"slug"`
	AppID         string   `json:"app_id,omitempty"`
	Status        string   `json:"status"`
	DeploymentIDs []string `json:"deployment_ids,omitempty"`
}
type fixture struct {
	Slug         string `json:"slug"`
	DeploymentID string `json:"deployment_id"`
	URL          string `json:"url"`
}
type config struct {
	APIURL        string             `json:"api_url"`
	Runtime       string             `json:"runtime"`
	Duration      int                `json:"duration_seconds"`
	Settle        int                `json:"settle_seconds"`
	Fixtures      map[string]fixture `json:"fixtures"`
	NativeCommand []string           `json:"native_restore_command"`
}

func save(path string, data any) error {
	body, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".profile-ci-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func archive(root, path, mode string) error {
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	add := func(name string, body []byte, perm int64) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: perm, Size: int64(len(body))}); err != nil {
			return err
		}
		_, err := tw.Write(body)
		return err
	}
	dockerfile := fmt.Sprintf(`FROM golang:1.25.13-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY pkg ./pkg
COPY tests/profiling/deployment/workload ./tests/profiling/deployment/workload
RUN CGO_ENABLED=0 go build -trimpath -o /app/server ./tests/profiling/deployment/workload
FROM debian:bookworm-slim
COPY --from=build /app/server /app/server
ENV PORT=8080 PROFILE_ACCEPTANCE_MODE=%s
EXPOSE 8080
ENTRYPOINT ["/app/server"]
`, mode)
	if err := add("Dockerfile", []byte(dockerfile), 0644); err != nil {
		return err
	}
	for _, name := range []string{"go.mod", "go.sum", "pkg", "tests/profiling/deployment/workload"} {
		err := filepath.WalkDir(filepath.Join(root, name), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" || d.Name() == "node_modules" || strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return add(filepath.ToSlash(relative), body, int64(info.Mode().Perm()))
		})
		if err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func live(ctx context.Context, c *api.Client, id string) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		dep, err := c.GetDeployment(ctx, id)
		if err != nil {
			return err
		}
		switch dep.Status {
		case "live":
			return nil
		case "failed", "cancelled", "rolled_back":
			return errors.New("fixture deployment failed")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func provision(ctx context.Context, c *api.Client, root, out, apiURL string) error {
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	j := journal{Slug: "prof-ci-" + hex.EncodeToString(nonce[:]), Status: "creating"}
	path := filepath.Join(out, "fixtures.json")
	if err := save(path, j); err != nil {
		return err
	}
	app, err := c.CreateApp(ctx, api.CreateAppRequest{Slug: j.Slug, Type: "app", Runtime: "go124", MaxConcurrency: 4, IdleTimeoutS: 600, Profiling: &api.ProfilingConfig{Enabled: true, WindowSeconds: 2}})
	if err != nil {
		return err
	}
	j.AppID = app.ID
	j.Status = "deploying"
	if err := save(path, j); err != nil {
		return err
	}
	routes := []api.DeclaredRoute{{Path: "/hot/{id}", Methods: []string{"GET"}}, {Path: "/readyz", Methods: []string{"GET"}}, {Path: "/healthz", Methods: []string{"GET"}}, {Path: "/acceptance/profile-state", Methods: []string{"GET"}}, {Path: "/acceptance/stale-profile", Methods: []string{"POST"}}}
	enabled := true
	if _, err := c.UpdateApp(ctx, j.Slug, api.UpdateAppRequest{DeclaredRoutes: &routes, RouteMetricsEnabled: &enabled}); err != nil {
		return err
	}
	if err := c.SetSecret(ctx, j.Slug, "GREGALE_NATIVE_PROFILE_TOKEN", os.Getenv("GREGALE_NATIVE_PROFILE_TOKEN")); err != nil {
		return err
	}
	cfg := config{APIURL: apiURL, Runtime: "go124", Duration: 120, Settle: 30, Fixtures: map[string]fixture{}}
	cfgPath := filepath.Join(out, "config.json")
	cfg.NativeCommand = []string{"python3", filepath.Join(root, "tests/profiling/deployment/native_restore.py"), "--config", cfgPath}
	for _, mode := range []string{"baseline", "regression", "label_loss", "sparse"} {
		source := filepath.Join(out, mode+".tar.gz")
		if err := archive(root, source, mode); err != nil {
			return err
		}
		file, err := os.Open(source)
		if err != nil {
			return err
		}
		traffic := 0
		if mode == "baseline" {
			traffic = 100
		}
		dep, err := c.DeployMultipart(ctx, j.Slug, file, mode+".tar.gz", "go124", "", true, api.DeployAnnotations{TrafficPercent: &traffic, Healthcheck: &api.DeploymentHealthcheck{Path: "/healthz"}, NoTriggers: true})
		file.Close()
		os.Remove(source)
		if err != nil {
			return err
		}
		j.DeploymentIDs = append(j.DeploymentIDs, dep.ID)
		if err := save(path, j); err != nil {
			return err
		}
		if err := live(ctx, c, dep.ID); err != nil {
			return err
		}
		url, err := c.GetDeploymentURL(ctx, dep.ID)
		if err != nil {
			return err
		}
		if !url.Alive || url.URL == "" {
			return errors.New("deployment-pinned gateway URL unavailable")
		}
		cfg.Fixtures[mode] = fixture{Slug: j.Slug, DeploymentID: dep.ID, URL: url.URL}
	}
	j.Status = "ready"
	if err := save(path, j); err != nil {
		return err
	}
	return save(cfgPath, cfg)
}

func cleanup(ctx context.Context, c *api.Client, out string) error {
	path := filepath.Join(out, "fixtures.json")
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return save(filepath.Join(out, "cleanup.json"), map[string]string{"status": "no_fixtures"})
	}
	if err != nil {
		return err
	}
	var j journal
	if err := json.Unmarshal(body, &j); err != nil {
		return err
	}
	if !regexp.MustCompile(`^prof-ci-[a-f0-9]{24}$`).MatchString(j.Slug) {
		return errors.New("invalid fixture journal slug")
	}
	app, err := c.GetApp(ctx, j.Slug)
	var problem *api.APIError
	if errors.As(err, &problem) && problem.Problem.Status == 404 {
		return save(filepath.Join(out, "cleanup.json"), map[string]string{"status": "already_absent"})
	}
	if err != nil {
		return err
	}
	if j.AppID != "" && app.ID != j.AppID {
		return errors.New("fixture ownership mismatch")
	}
	if err := c.DeleteApp(ctx, j.Slug); err != nil {
		return err
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		rows, err := c.ListInstances(ctx, j.Slug)
		if errors.As(err, &problem) && problem.Problem.Status == 404 {
			break
		}
		if err != nil {
			return err
		}
		resident := false
		for _, row := range rows {
			resident = resident || row.Resident
		}
		if !resident {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	return save(filepath.Join(out, "cleanup.json"), map[string]string{"status": "deletion_scheduled_and_drained", "slug": j.Slug})
}

func main() {
	action := flag.String("action", "provision", "provision or cleanup")
	root := flag.String("root", ".", "checkout root")
	out := flag.String("out", "profile-native-evidence", "evidence directory")
	flag.Parse()
	absolute, err := filepath.Abs(*root)
	if err != nil {
		os.Exit(1)
	}
	target, err := filepath.Abs(*out)
	if err != nil {
		os.Exit(1)
	}
	apiURL, token := os.Getenv("GREGALE_ACCEPTANCE_API_URL"), os.Getenv("GREGALE_ACCEPTANCE_TOKEN")
	if apiURL == "" || token == "" || (*action == "provision" && len(os.Getenv("GREGALE_NATIVE_PROFILE_TOKEN")) < 32) {
		fmt.Fprintln(os.Stderr, "profiling CI: required configuration is missing")
		os.Exit(1)
	}
	client := api.NewClient(apiURL, token)
	budget := 25 * time.Minute
	if *action == "cleanup" {
		budget = 3 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	switch *action {
	case "provision":
		err = provision(ctx, client, absolute, target, apiURL)
	case "cleanup":
		err = cleanup(ctx, client, target)
	default:
		err = errors.New("invalid action")
	}
	if err != nil {
		_ = save(filepath.Join(target, *action+"-error.json"), map[string]string{"status": "failed", "error_type": fmt.Sprintf("%T", err)})
		fmt.Fprintln(os.Stderr, "profiling CI:", *action, "failed; see evidence")
		os.Exit(1)
	}
}
