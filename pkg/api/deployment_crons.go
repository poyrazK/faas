package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/cronexpr"
)

const CodeDeploymentCronConflict = "deployment_cron_conflict"

func ValidateDeploymentCrons(rows []DeploymentCronConfig) error {
	if rows == nil {
		return fmt.Errorf("cron set must be an explicit array")
	}
	ids, keys := map[string]bool{}, map[string]bool{}
	for _, row := range rows {
		id, err := uuid.Parse(row.ID)
		if err != nil || id == uuid.Nil || id.String() != row.ID {
			return fmt.Errorf("invalid cron identity")
		}
		key := row.Schedule + "\x00" + row.Path
		if ids[row.ID] || keys[key] {
			return fmt.Errorf("duplicate cron identity or schedule/path")
		}
		ids[row.ID], keys[key] = true, true
		if cronexpr.Validate(row.Schedule) != nil || !strings.HasPrefix(row.Path, "/") {
			return fmt.Errorf("invalid HTTP cron schedule or path")
		}
		if row.Timezone == "" {
			return fmt.Errorf("cron timezone is required")
		}
		if _, err := time.LoadLocation(row.Timezone); err != nil {
			return fmt.Errorf("invalid cron timezone: %w", err)
		}
	}
	return nil
}

func (r DeploymentCronStagingRequest) Validate() error {
	if !ValidDeploymentAttemptID(r.SessionID) || r.ExpectedAppID == "" || r.ExpectedRevision < 0 || r.ExpectedRevision == math.MaxInt64 || (r.Operation != "stage" && r.Operation != "restore" && r.Operation != "prepare" && r.Operation != "cancel" && r.Operation != "track_upload") {
		return fmt.Errorf("invalid cron staging identity, revision or operation")
	}
	if r.Operation == "track_upload" {
		valid := len(r.UploadID) > 0 && len(r.UploadID) <= 128 && strings.Trim(r.UploadID, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_") == ""
		if !valid {
			return fmt.Errorf("invalid upload identity")
		}
	} else if r.UploadID != "" {
		return fmt.Errorf("upload identity requires track_upload")
	}
	if r.Environment != "" {
		if ValidateScope(r.Environment) != nil || r.Environment == "production" || r.Environment == "default" {
			return fmt.Errorf("invalid cron staging environment")
		}
		if r.Operation == "stage" || r.Operation == "restore" {
			return fmt.Errorf("an environment's crons are staged with prepare")
		}
	}
	if err := ValidateDeploymentCrons(r.Before); err != nil {
		return err
	}
	if err := ValidateDeploymentCrons(r.Desired); err != nil {
		return err
	}
	if r.Operation != "stage" && r.Operation != "prepare" {
		// Restoring, cancelling and tracking replay an intent already
		// staged; one staged before the stricter rules must still undo.
		return nil
	}
	return validateStagedCronChanges(r.Before, r.Desired)
}

// validateStagedCronChanges applies the cron API's path and timezone rules to
// every desired cron the request adds or changes. Staging checked only a
// leading "/" and a loadable zone, so it stored paths the cron API refuses
// ("//host", spaces, "#", over CronPathMaxBytes) and the host-dependent
// "Local" zone (crons audit 2026-11-03). A row kept with the path and zone
// it had before stays valid, so a cron stored under older rules is carried.
func validateStagedCronChanges(before, desired []DeploymentCronConfig) error {
	kept := make(map[string]DeploymentCronConfig, len(before))
	for _, row := range before {
		kept[row.ID] = row
	}
	for _, row := range desired {
		if old, ok := kept[row.ID]; ok && old.Path == row.Path && old.Timezone == row.Timezone {
			continue
		}
		if problem := ValidateCronPath(row.Path); problem != nil {
			return fmt.Errorf("invalid HTTP cron path %q: %s", row.Path, problem.Detail)
		}
		if _, err := cronexpr.NormalizeCustomerTimezone(row.Timezone); err != nil {
			return fmt.Errorf("invalid cron timezone: %w", err)
		}
	}
	return nil
}

func orderedDeploymentCrons(rows []DeploymentCronConfig) []DeploymentCronConfig {
	out := append([]DeploymentCronConfig{}, rows...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func DeploymentCronsEqual(a, b []DeploymentCronConfig) bool {
	return reflect.DeepEqual(orderedDeploymentCrons(a), orderedDeploymentCrons(b))
}

func (r DeploymentCronStagingRequest) Fingerprint() string {
	r.Operation, r.UploadID = "", ""
	r.Before, r.Desired = orderedDeploymentCrons(r.Before), orderedDeploymentCrons(r.Desired)
	data, _ := json.Marshal(r)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (c *Client) GetDeploymentCronStagingSnapshot(ctx context.Context, slug string) (DeploymentCronStagingSnapshot, error) {
	return c.GetEnvironmentDeploymentCronStagingSnapshot(ctx, slug, "")
}

// GetEnvironmentDeploymentCronStagingSnapshot reads the manifest HTTP crons of
// one environment; an empty environment is production.
func (c *Client) GetEnvironmentDeploymentCronStagingSnapshot(ctx context.Context, slug, environment string) (DeploymentCronStagingSnapshot, error) {
	var out DeploymentCronStagingSnapshot
	path := "/v1/apps/" + slug + "/deployment-cron-staging"
	if environment != "" {
		path += "?environment=" + url.QueryEscape(environment)
	}
	err := c.do(ctx, "GET", path, nil, &out)
	return out, err
}

func (c *Client) ApplyDeploymentCronStaging(ctx context.Context, slug string, request DeploymentCronStagingRequest) (DeploymentStagingReceipt, error) {
	var out DeploymentStagingReceipt
	err := c.do(singleDeploymentAttemptContext(ctx), "POST", "/v1/apps/"+slug+"/deployment-cron-staging", request, &out)
	return out, err
}
