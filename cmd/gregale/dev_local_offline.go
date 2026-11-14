package main

// `gregale dev --local --offline` (ADR-20261107210000 slice 3): local
// stand-ins in Docker instead of the developer session's resources. Owner
// decisions 2026-11-07: Postgres 16 and the Versity S3 Gateway (Apache-2.0),
// data kept in per-project volumes between runs, --reset wipes it.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

const (
	devOfflinePostgresImage = "postgres:16"
	devOfflineS3Image       = "versity/versitygw:v1.10.0"
	devOfflineUser          = "gregale"
	devOfflinePassword      = "gregale-local"
	devOfflineS3Access      = "gregale-local"
	devOfflineS3Secret      = "gregale-local-secret"
	devOfflineS3Region      = "us-east-1"
	devOfflineReadyTimeout  = 90 * time.Second
)

var devOfflineBucketRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)

// devOfflineOptions are what the project asked to run locally.
type devOfflineOptions struct {
	Postgres bool
	Buckets  []string
	Reset    bool
}

// devOfflineNames are the project's container and volume names.
type devOfflineNames struct{ Postgres, S3 string }

func offlineNames(projectDir string) devOfflineNames {
	sum := sha256.Sum256([]byte(projectDir))
	base := "gregale-dev-" + localName(projectDir)
	if len(base) > 40 {
		base = base[:40]
	}
	base += "-" + hex.EncodeToString(sum[:4])
	return devOfflineNames{Postgres: base + "-pg", S3: base + "-s3"}
}

// offlineOptionsFor merges the flags with the dev: block of gregale.yaml.
func offlineOptionsFor(projectDir string, postgres bool, buckets []string, reset bool) (devOfflineOptions, error) {
	opts := devOfflineOptions{Postgres: postgres, Reset: reset}
	if m, found, err := gregalemanifest.Load(projectDir); err == nil && found && m.Dev != nil {
		if m.Dev.Postgres != nil && *m.Dev.Postgres {
			opts.Postgres = true
		}
		for _, b := range m.Dev.Buckets {
			buckets = append(buckets, b.Name)
		}
	}
	seen := map[string]bool{}
	for _, b := range buckets {
		if seen[b] {
			continue
		}
		if !devOfflineBucketRE.MatchString(b) {
			return opts, fmt.Errorf("bucket %q: use 3-63 lowercase letters, digits and hyphens", b)
		}
		seen[b] = true
		opts.Buckets = append(opts.Buckets, b)
	}
	return opts, nil
}

func postgresRunArgs(n devOfflineNames) []string {
	return []string{"run", "-d", "--rm", "--name", n.Postgres,
		"-e", "POSTGRES_USER=" + devOfflineUser, "-e", "POSTGRES_PASSWORD=" + devOfflinePassword, "-e", "POSTGRES_DB=" + devOfflineUser,
		"-p", "127.0.0.1::5432", "-v", n.Postgres + ":/var/lib/postgresql/data", devOfflinePostgresImage}
}

func s3RunArgs(n devOfflineNames) []string {
	return []string{"run", "-d", "--rm", "--name", n.S3,
		"-e", "ROOT_ACCESS_KEY=" + devOfflineS3Access, "-e", "ROOT_SECRET_KEY=" + devOfflineS3Secret,
		"-p", "127.0.0.1::7070", "-v", n.S3 + ":/data", devOfflineS3Image, "--port", ":7070", "posix", "/data"}
}

func offlinePostgresURL(port string) string {
	return "postgresql://" + devOfflineUser + ":" + devOfflinePassword + "@127.0.0.1:" + port + "/" + devOfflineUser + "?sslmode=disable"
}

// offlineBucketEnv is the GREGALE_S3_* set a bucket binding would inject.
func offlineBucketEnv(bucket, endpoint string) map[string]string {
	prefix := api.DefaultObjectStorageBindingPrefix(bucket)
	return map[string]string{
		prefix + "_ENDPOINT": endpoint, prefix + "_REGION": devOfflineS3Region, prefix + "_BUCKET": bucket,
		prefix + "_ACCESS_KEY_ID": devOfflineS3Access, prefix + "_SECRET_ACCESS_KEY": devOfflineS3Secret,
		prefix + "_ADDRESSING_STYLE": "path",
	}
}

// startDevLocalOffline starts the stand-ins and returns their settings.
func startDevLocalOffline(ctx context.Context, projectDir string, opts devOfflineOptions, progress io.Writer) (*devLocalRemote, error) {
	r := &devLocalRemote{Env: map[string]string{}}
	if !opts.Postgres && len(opts.Buckets) == 0 {
		r.note("Offline: no database or buckets requested; pass --postgres or --bucket NAME")
		return r, nil
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return nil, errors.New("--offline runs Postgres and buckets in Docker, and docker is not installed")
	}
	if _, err := docker(ctx, "info", "--format", "{{.ServerVersion}}"); err != nil {
		return nil, errors.New("--offline needs Docker running; start it and try again")
	}
	n := offlineNames(projectDir)
	if opts.Reset {
		_, _ = docker(ctx, "rm", "-f", n.Postgres, n.S3)
		_, _ = docker(ctx, "volume", "rm", "-f", n.Postgres, n.S3)
		r.note("Offline: wiped the local database and bucket files")
	}
	if opts.Postgres {
		if err := r.startOfflinePostgres(ctx, n, progress); err != nil {
			r.close()
			return nil, err
		}
	}
	if len(opts.Buckets) > 0 {
		if err := r.startOfflineS3(ctx, n, opts.Buckets, progress); err != nil {
			r.close()
			return nil, err
		}
	}
	return r, nil
}

func (r *devLocalRemote) startOfflinePostgres(ctx context.Context, n devOfflineNames, progress io.Writer) error {
	_, _ = fmt.Fprintf(progress, "Starting Postgres in Docker (%s)...\n", devOfflinePostgresImage)
	port, err := runOfflineContainer(ctx, n.Postgres, postgresRunArgs(n), "5432/tcp")
	if err != nil {
		return fmt.Errorf("start Postgres: %w", err)
	}
	r.cleanup = append(r.cleanup, func() { _, _ = docker(context.WithoutCancel(ctx), "stop", n.Postgres) })
	deadline := time.Now().Add(devOfflineReadyTimeout)
	for {
		if _, err := docker(ctx, "exec", n.Postgres, "pg_isready", "-U", devOfflineUser, "-d", devOfflineUser); err == nil {
			break
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return errors.New("Postgres did not become ready; see docker logs " + n.Postgres)
		}
		time.Sleep(500 * time.Millisecond)
	}
	r.Env["DATABASE_URL"] = offlinePostgresURL(port)
	r.note("Offline: DATABASE_URL is local Postgres (volume %s, kept between runs; --reset wipes it)", n.Postgres)
	return nil
}

func (r *devLocalRemote) startOfflineS3(ctx context.Context, n devOfflineNames, buckets []string, progress io.Writer) error {
	_, _ = fmt.Fprintf(progress, "Starting an S3 gateway in Docker (%s)...\n", devOfflineS3Image)
	port, err := runOfflineContainer(ctx, n.S3, s3RunArgs(n), "7070/tcp")
	if err != nil {
		return fmt.Errorf("start the S3 gateway: %w", err)
	}
	r.cleanup = append(r.cleanup, func() { _, _ = docker(context.WithoutCancel(ctx), "stop", n.S3) })
	endpoint := "http://127.0.0.1:" + port
	if err := waitTCP(ctx, "127.0.0.1:"+port, devOfflineReadyTimeout); err != nil {
		return errors.New("the S3 gateway did not start; see docker logs " + n.S3)
	}
	client := s3.New(s3.Options{
		Region: devOfflineS3Region, BaseEndpoint: aws.String(endpoint), UsePathStyle: true,
		Credentials: credentials.NewStaticCredentialsProvider(devOfflineS3Access, devOfflineS3Secret, ""),
	})
	for _, b := range buckets {
		if err := createOfflineBucket(ctx, client, b); err != nil {
			return fmt.Errorf("create bucket %s: %w", b, err)
		}
		for k, v := range offlineBucketEnv(b, endpoint) {
			r.Env[k] = v
		}
	}
	r.note("Offline: buckets %s are local (volume %s, kept between runs)", strings.Join(buckets, ", "), n.S3)
	return nil
}

func createOfflineBucket(ctx context.Context, client *s3.Client, bucket string) error {
	var lastErr error
	for range 20 {
		if _, err := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)}); err == nil {
			return nil
		}
		_, lastErr = client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)})
		if lastErr == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		time.Sleep(500 * time.Millisecond)
	}
	return lastErr
}

// runOfflineContainer replaces a leftover container of the same name, starts
// one, and returns its published loopback port.
func runOfflineContainer(ctx context.Context, name string, args []string, containerPort string) (string, error) {
	_, _ = docker(ctx, "rm", "-f", name)
	if _, err := docker(ctx, args...); err != nil {
		return "", err
	}
	out, err := docker(ctx, "port", name, containerPort)
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	_, port, err := net.SplitHostPort(strings.TrimSpace(line))
	if err != nil {
		return "", fmt.Errorf("unexpected docker port output %q", out)
	}
	return port, nil
}

func docker(ctx context.Context, args ...string) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "docker", args...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker %s: %w: %s", args[0], err, strings.TrimSpace(errOut.String()))
	}
	return out.String(), nil
}

func waitTCP(ctx context.Context, addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		var d net.Dialer
		dialCtx, cancel := context.WithTimeout(ctx, time.Second)
		conn, err := d.DialContext(dialCtx, "tcp", addr)
		cancel()
		if err == nil {
			_ = conn.Close()
			return nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return err
		}
		time.Sleep(300 * time.Millisecond)
	}
}
