package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdRunDeliveries(args []string) int {
	if len(args) == 0 {
		return printErr("Delivery command required", fmt.Errorf("use deliver, status or download-url"))
	}
	verb := args[0]
	fs := newFlagSet("run deliveries", flag.ContinueOnError)
	run := fs.String("run", "", "successful Run UUID")
	artifact := fs.String("artifact", "", "declared output file name")
	contentType := fs.String("content-type", "application/octet-stream", "destination content type")
	key := fs.String("idempotency-key", "", "stable storage delivery key; required for deliver")
	wait := fs.Bool("wait", false, "wait for the storage receipt to settle")
	timeout := fs.Duration("wait-timeout", 3*time.Minute, "maximum client wait; never retries a provider write")
	flags, pos := splitArgsForFlags(args[1:], "wait")
	if fs.Parse(flags) != nil {
		return 1
	}
	if *timeout <= 0 || verb == "deliver" && (len(pos) != 2 || *run == "" || *artifact == "" || *key == "") || verb != "deliver" && (len(pos) != 3 || verb != "status" && verb != "download-url") {
		return printErr("Invalid delivery arguments", fmt.Errorf("deliver APP ROUTE --run UUID --artifact NAME --idempotency-key KEY [--wait]; status/download-url APP ROUTE DELIVERY_UUID"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if verb == "download-url" {
		out, err := client.SignRunArtifactDelivery(ctx, pos[0], pos[1], pos[2])
		if err != nil {
			return printErr("Download URL unavailable", err)
		}
		return jsonOut(writeJSON(out))
	}
	var out api.RunArtifactDelivery
	if verb == "deliver" {
		out, err = createRunArtifactDelivery(ctx, client, pos[0], pos[1], *run, *artifact, *contentType, *key)
	} else {
		out, err = client.GetRunArtifactDelivery(ctx, pos[0], pos[1], pos[2])
	}
	if err != nil {
		return printErr("Artifact delivery failed", err)
	}
	if *wait {
		out, err = waitRunArtifactDelivery(ctx, client, pos[0], pos[1], out)
	}
	if err != nil {
		_ = writeJSON(out)
		return printErr("Delivery wait stopped; inspect the same receipt", err)
	}
	if code := jsonOut(writeJSON(out)); code != 0 {
		return code
	}
	if out.Status == "failed" {
		return 1
	}
	return 0
}

func createRunArtifactDelivery(ctx context.Context, client *api.Client, app, route, run, name, contentType, key string) (api.RunArtifactDelivery, error) {
	row, err := client.GetExecution(ctx, run)
	if err != nil {
		return api.RunArtifactDelivery{}, err
	}
	if row.Status != api.ExecutionStatusSucceeded || row.ResultValidation != nil && row.ResultValidation.Status == "invalid" {
		return api.RunArtifactDelivery{}, fmt.Errorf("delivery requires a successful Run with a valid result contract")
	}
	for _, artifact := range row.Artifacts {
		if artifact.Name == name {
			return client.DeliverRunArtifact(ctx, app, route, api.DeliverRunArtifactRequest{IdempotencyKey: key, ExecutionID: row.ID, ArtifactName: name, SizeBytes: int64(artifact.SizeBytes), SHA256: artifact.SHA256, ContentType: contentType})
		}
	}
	return api.RunArtifactDelivery{}, fmt.Errorf("run has no declared artifact named %q", name)
}

func waitRunArtifactDelivery(ctx context.Context, client *api.Client, app, route string, out api.RunArtifactDelivery) (api.RunArtifactDelivery, error) {
	for out.Status == "pending" {
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-time.After(api.ObjectUploadRecoveryRetry):
		}
		next, err := client.GetRunArtifactDelivery(ctx, app, route, out.ID)
		if err != nil {
			return out, err
		}
		out = next
	}
	return out, nil
}
