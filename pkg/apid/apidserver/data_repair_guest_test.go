package apidserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	core "github.com/onebox-faas/faas/pkg/datarepair"
	repairguest "github.com/onebox-faas/faas/pkg/datarepair/guest"
)

func runRepairGuestFixture(ctx context.Context, helper string, in core.RehearsalInput) (evidence core.Evidence, result error) {
	name := "gregale-repair-guest-" + uuid.NewString()
	stage := name + "-install"
	var imageID string
	var created, staged bool
	defer func() {
		clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		for container, exists := range map[string]bool{name: created, stage: staged} {
			if exists && exec.CommandContext(clean, "docker", "rm", "--force", container).Run() != nil {
				result = errors.Join(result, errors.New("guest fixture cleanup failed"))
			}
		}
		if imageID != "" && exec.CommandContext(clean, "docker", "image", "rm", imageID).Run() != nil {
			result = errors.Join(result, errors.New("guest fixture image cleanup failed"))
		}
	}()
	// Package the fixed platform test executable before creating the read-only
	// runtime. This stages no customer build inputs or commands on the host.
	if out, err := exec.CommandContext(ctx, "docker", "create", "--name", stage, "--label", "gregale.data-repair=guest-acceptance", "--network=none", core.DefaultSandboxImage).CombinedOutput(); err != nil {
		return evidence, fmt.Errorf("guest fixture staging: %s", out)
	}
	staged = true
	if exec.CommandContext(ctx, "docker", "cp", helper, stage+":/repair-guest-test").Run() != nil {
		return evidence, errors.New("guest fixture copy failed")
	}
	image, err := exec.CommandContext(ctx, "docker", "commit", stage).Output()
	if err != nil {
		return evidence, errors.New("guest fixture image failed")
	}
	imageID = strings.TrimSpace(string(image))
	args := []string{"create", "--name", name, "--label", "gregale.data-repair=guest-acceptance", "--network=none", "--read-only", "--cap-drop=ALL", "--cap-add=CHOWN", "--cap-add=SETUID", "--cap-add=SETGID", "--cap-add=DAC_OVERRIDE", "--cap-add=KILL", "--security-opt=no-new-privileges", "--memory=" + strconv.Itoa(api.DataRepairSandboxMemoryMB) + "m", "--cpus=1", "--pids-limit=64", "--tmpfs", "/tmp:rw,nosuid,nodev,size=" + strconv.Itoa(api.DataRepairSandboxDiskBytes), "-e", "GREGALE_REPAIR_GUEST_HELPER=1", "--interactive", "--entrypoint", "/repair-guest-test", imageID, "-test.run=^TestRehearsalGuestProcess$", "-test.timeout=125s"}
	if out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput(); err != nil {
		return evidence, fmt.Errorf("guest fixture create: %s", out)
	}
	created = true
	var input bytes.Buffer
	if err := core.WriteRehearsalInput(&input, in); err != nil {
		return evidence, err
	}
	cmd := exec.CommandContext(ctx, "docker", "start", "--attach", "--interactive", name)
	cmd.Stdin = &input
	out, err := cmd.CombinedOutput()
	if err != nil {
		return evidence, fmt.Errorf("guest fixture process failed: %s", out)
	}
	var response repairguest.Result
	if json.Unmarshal(out, &response) != nil || response.Evidence == nil || response.FailureCode != "" {
		return evidence, fmt.Errorf("guest fixture failed: %s", strings.TrimSpace(string(out)))
	}
	if !in.Plan.ValidCheckResults(*response.Evidence) || response.Evidence.SnapshotSHA256 != in.SnapshotSHA256 || response.Evidence.PlanDigest != in.Plan.Digest {
		return evidence, errors.New("guest fixture returned different evidence")
	}
	return *response.Evidence, nil
}
