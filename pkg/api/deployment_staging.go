package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
)

const CodeDeploymentStagingConflict = "deployment_staging_conflict"

// ValidatePolicySettings closes the saved journal over two non-secret types.
func ValidatePolicySettings(settings map[string]json.RawMessage) error {
	for field, value := range settings {
		if _, err := decodePolicySetting(field, value); err != nil {
			return err
		}
	}
	return nil
}

func decodePolicySetting(field string, value json.RawMessage) (any, error) {
	var out any
	switch field {
	case "profiling":
		out = new(*ProfilingConfig)
	case "tracing":
		out = new(*TracingConfig)
	default:
		return nil, fmt.Errorf("unsupported deployment policy field %q", field)
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return nil, fmt.Errorf("invalid %s policy: %w", field, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("invalid %s policy encoding", field)
	}
	return reflect.ValueOf(out).Elem().Interface(), nil
}

func PolicySettingEqual(field string, a, b json.RawMessage) bool {
	left, err := decodePolicySetting(field, a)
	if err != nil {
		return false
	}
	right, err := decodePolicySetting(field, b)
	if err != nil {
		return false
	}
	return reflect.DeepEqual(left, right)
}

func (r DeploymentStagingRequest) Validate() error {
	if !ValidDeploymentAttemptID(r.SessionID) || r.ExpectedAppID == "" || r.ExpectedRevision < 0 || (r.Operation != "stage" && r.Operation != "restore") {
		return fmt.Errorf("invalid deployment staging identity, revision or operation")
	}
	if len(r.Desired) == 0 || len(r.Before) != len(r.Desired) {
		return fmt.Errorf("deployment policy journal must contain matching before and desired fields")
	}
	for field := range r.Desired {
		if _, found := r.Before[field]; !found {
			return fmt.Errorf("missing before policy for %s", field)
		}
	}
	if err := ValidatePolicySettings(r.Before); err != nil {
		return err
	}
	return ValidatePolicySettings(r.Desired)
}

// Fingerprint excludes operation so restore authenticates the exact stage intent.
func (r DeploymentStagingRequest) Fingerprint() (string, error) {
	r.Operation = ""
	copySettings := func(source map[string]json.RawMessage) (map[string]json.RawMessage, error) {
		out := make(map[string]json.RawMessage, len(source))
		for field, value := range source {
			decoded, err := decodePolicySetting(field, value)
			if err != nil {
				return nil, err
			}
			out[field], err = json.Marshal(decoded)
			if err != nil {
				return nil, err
			}
		}
		return out, nil
	}
	var err error
	r.Before, err = copySettings(r.Before)
	if err != nil {
		return "", err
	}
	r.Desired, err = copySettings(r.Desired)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

func (c *Client) GetDeploymentStagingSnapshot(ctx context.Context, slug string) (DeploymentStagingSnapshot, error) {
	var out DeploymentStagingSnapshot
	err := c.do(ctx, "GET", "/v1/apps/"+slug+"/deployment-staging", nil, &out)
	return out, err
}

func (c *Client) ApplyDeploymentStaging(ctx context.Context, slug string, request DeploymentStagingRequest) (DeploymentStagingReceipt, error) {
	var out DeploymentStagingReceipt
	err := c.do(singleDeploymentAttemptContext(ctx), "POST", "/v1/apps/"+slug+"/deployment-staging", request, &out)
	return out, err
}
