package e2etest

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// EventDeliveryConsumerImage runs the same token-controlled staging consumer
// in a real Linux/amd64 guest. The caller supplies its fresh control token via
// deployment environment overrides. Only the OCI registry is a local fixture;
// VMMD, guest boot and HTTP delivery use the native runtime.
func EventDeliveryConsumerImage(ctx context.Context, repo string) (fakeImage, string, error) {
	dir, err := os.MkdirTemp("", "faas-e2e-event-consumer-*")
	if err != nil {
		return fakeImage{}, "", fmt.Errorf("event consumer: temporary directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		return fakeImage{}, "", fmt.Errorf("event consumer: cannot locate source")
	}
	out := filepath.Join(dir, "event-consumer")
	cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-ldflags=-s -w", "-o", out, "./scripts/ops/eventdelivery-consumer")
	cmd.Dir = filepath.Join(filepath.Dir(source), "..", "..")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fakeImage{}, "", fmt.Errorf("event consumer: build: %w\n%s", err, output)
	}
	binary, err := os.ReadFile(out)
	if err != nil {
		return fakeImage{}, "", fmt.Errorf("event consumer: read executable: %w", err)
	}
	image, ref := staticFixtureImage(repo, "event-consumer", binary, "amd64", []string{"/event-consumer"}, 8080)
	return image, ref, nil
}
