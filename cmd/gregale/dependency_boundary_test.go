package main_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The CLI ships to customers as one download, so every package it links is
// paid for in download size and in init time on every invocation, including
// each shell completion. Manifest validation once imported pkg/sched for one
// cron parser, which linked the scheduler, the Firecracker driver, Kafka and
// Redis clients into the CLI (9 MiB of a 100 MiB binary). The rule is
// transitive, which depguard's direct-import rules cannot express. Add a
// package here once the CLI stops linking it.
func TestCLILinksNoRuntimePackages(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	forbidden := []string{
		"github.com/onebox-faas/faas/pkg/apislogs",
		"github.com/onebox-faas/faas/pkg/builderd",
		"github.com/onebox-faas/faas/pkg/fcvm",
		"github.com/onebox-faas/faas/pkg/imaged",
		"github.com/onebox-faas/faas/pkg/meter",
		"github.com/onebox-faas/faas/pkg/rootfs",
		"github.com/onebox-faas/faas/pkg/sched",
		"github.com/onebox-faas/faas/pkg/scheddgrpc",
		"github.com/onebox-faas/faas/pkg/vmmd",
		"github.com/nats-io/nats.go",
		"github.com/rabbitmq/amqp091-go",
		"github.com/redis/go-redis",
		"github.com/segmentio/kafka-go",
	}
	// Standard-library-only leaves the CLI shares with the runtime.
	allowed := map[string]bool{"github.com/onebox-faas/faas/pkg/fcvm/logbuf": true}
	// The release workflow builds the CLI for these targets.
	for _, goos := range []string{"linux", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			cmd := exec.Command(goBin, "list", "-deps", ".")
			cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH=amd64", "CGO_ENABLED=0")
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("go list -deps: %v", err)
			}
			for _, dep := range strings.Fields(string(out)) {
				if allowed[dep] {
					continue
				}
				for _, f := range forbidden {
					if dep == f || strings.HasPrefix(dep, f+"/") {
						t.Errorf("gregale links %s; keep runtime packages out of the CLI", dep)
					}
				}
			}
		})
	}
}
