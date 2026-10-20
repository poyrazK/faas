package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
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
	if err := ValidateDeploymentCrons(r.Before); err != nil {
		return err
	}
	return ValidateDeploymentCrons(r.Desired)
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
	var out DeploymentCronStagingSnapshot
	err := c.do(ctx, "GET", "/v1/apps/"+slug+"/deployment-cron-staging", nil, &out)
	return out, err
}

func (c *Client) ApplyDeploymentCronStaging(ctx context.Context, slug string, request DeploymentCronStagingRequest) (DeploymentStagingReceipt, error) {
	var out DeploymentStagingReceipt
	err := c.do(singleDeploymentAttemptContext(ctx), "POST", "/v1/apps/"+slug+"/deployment-cron-staging", request, &out)
	return out, err
}
