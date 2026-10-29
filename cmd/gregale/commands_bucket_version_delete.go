package main

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Explicit version selection is the confirmation: this command permanently
// removes exactly that immutable data version or marker.
func cmdBucketVersionDelete(args []string) int {
	usage := "usage: gregale bucket version delete <app> <bucket-id> <key> <version-id>"
	// Scripts pass --yes to every destructive command; here it was counted
	// as a fifth argument and answered with a bare usage line (H10-130).
	args = withoutConfirmationFlags(args)
	if len(args) != 4 || !api.ValidAppSlug(args[0]) || args[2] == "" {
		PrintUsage(osStderr, usage, "bucket")
		return 1
	}
	for _, id := range []string{args[1], args[3]} {
		u, err := uuid.Parse(id)
		if err != nil || u.String() != id || u.Version() != 4 {
			PrintUsage(osStderr, usage, "bucket")
			return 1
		}
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := c.DeleteObjectBucketVersion(context.Background(), args[0], args[1], args[2], args[3])
	if err != nil {
		return printErr("Could not delete object version", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Deleted version %s. Capacity is reclaimed after verified inventory.\n", out.VersionID)
	return 0
}

// withoutConfirmationFlags drops --yes, -q and --quiet. Naming the exact
// version is this command's confirmation, so they change nothing.
func withoutConfirmationFlags(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		switch a {
		case "--yes", "-yes", "--quiet", "-quiet", "-q":
			continue
		}
		out = append(out, a)
	}
	return out
}
