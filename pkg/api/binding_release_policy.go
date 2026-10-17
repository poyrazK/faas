package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const CodeBindingReleaseRequired = "bindings_release_required"
const CodeBindingReleasePolicyChanged = "bindings_release_policy_changed"

// BindingReleasePolicy applies to every traffic increase in one deployment scope.
type BindingReleasePolicy struct {
	AppID                     string            `json:"app_id"`
	Scope                     string            `json:"scope"`
	Mode                      string            `json:"mode"`
	Revision                  int64             `json:"revision"`
	MaxVerificationAge        string            `json:"max_verification_age"`
	RequireApplicationAck     bool              `json:"require_application_ack"`
	ObjectStorageRequirements map[string]string `json:"object_storage_requirements,omitempty"`
	QueueRequirements         map[string]string `json:"queue_requirements,omitempty"`
	UpdatedAt                 *time.Time        `json:"updated_at,omitempty"`
	Reason                    string            `json:"reason,omitempty"`
}

type SetBindingReleasePolicyRequest struct {
	Mode                      string            `json:"mode"`
	ExpectedRevision          *int64            `json:"expected_revision"`
	MaxVerificationAge        string            `json:"max_verification_age,omitempty"`
	RequireApplicationAck     bool              `json:"require_application_ack"`
	ObjectStorageRequirements map[string]string `json:"object_storage_requirements,omitempty"`
	QueueRequirements         map[string]string `json:"queue_requirements,omitempty"`
	Reason                    string            `json:"reason,omitempty"`
}

// The dedicated route requires an explicit map, including {} when clearing.
type SetObjectStorageBindingReleasePolicyRequest struct {
	SetBindingReleasePolicyRequest
	ObjectStorageRequirements map[string]string `json:"object_storage_requirements"`
}

// The dedicated route requires an explicit map, including {} when clearing.
type SetQueueBindingReleasePolicyRequest struct {
	SetBindingReleasePolicyRequest
	QueueRequirements map[string]string `json:"queue_requirements"`
}

func ValidateBindingReleasePolicyRequest(r SetBindingReleasePolicyRequest) error {
	if err := ValidateObjectStorageRequirements(r.ObjectStorageRequirements); err != nil {
		return err
	}
	if err := ValidateQueueReleaseRequirements(r.QueueRequirements); err != nil {
		return err
	}
	if (r.Mode != "off" && r.Mode != "enforce") || r.ExpectedRevision == nil || *r.ExpectedRevision < 0 || *r.ExpectedRevision >= BindingReleasePolicyMaxRevision {
		return fmt.Errorf("supply mode off or enforce and expected_revision (0 initially)")
	}
	if len(r.Reason) > BindingReleasePolicyReasonMaxBytes || !utf8.ValidString(r.Reason) || strings.IndexFunc(r.Reason, unicode.IsControl) >= 0 {
		return fmt.Errorf("reason must be one line of at most %d bytes", BindingReleasePolicyReasonMaxBytes)
	}
	if r.Mode == "off" && strings.TrimSpace(r.Reason) == "" {
		return fmt.Errorf("a reason is required to disable release enforcement")
	}
	if r.MaxVerificationAge != "" {
		age, err := time.ParseDuration(r.MaxVerificationAge)
		if err != nil || age < time.Second || age > BindingReleasePolicyMaxAge || age%time.Second != 0 {
			return fmt.Errorf("max_verification_age must be a whole number of seconds between 1s and 24h")
		}
	}
	return nil
}

func bindingReleasePolicyPath(slug, scope string) string {
	return "/v1/apps/" + url.PathEscape(slug) + "/bindings/release-policy?scope=" + url.QueryEscape(scope)
}
func (c *Client) GetBindingReleasePolicy(ctx context.Context, slug, scope string) (BindingReleasePolicy, error) {
	var result BindingReleasePolicy
	err := c.do(ctx, http.MethodGet, bindingReleasePolicyPath(slug, scope), nil, &result)
	return result, err
}
func (c *Client) SetBindingReleasePolicy(ctx context.Context, slug, scope string, request SetBindingReleasePolicyRequest) (BindingReleasePolicy, error) {
	if request.QueueRequirements != nil {
		return c.SetQueueBindingReleasePolicy(ctx, slug, scope, SetQueueBindingReleasePolicyRequest{request, request.QueueRequirements})
	}
	if request.ObjectStorageRequirements != nil {
		return c.SetObjectStorageBindingReleasePolicy(ctx, slug, scope, SetObjectStorageBindingReleasePolicyRequest{request, request.ObjectStorageRequirements})
	}
	var result BindingReleasePolicy
	err := c.do(ctx, http.MethodPut, bindingReleasePolicyPath(slug, scope), request, &result)
	return result, err
}

func (c *Client) SetQueueBindingReleasePolicy(ctx context.Context, slug, scope string, request SetQueueBindingReleasePolicyRequest) (BindingReleasePolicy, error) {
	var result BindingReleasePolicy
	if request.QueueRequirements == nil {
		return result, fmt.Errorf("supply explicit queue_requirements (an empty object clears them)")
	}
	path := "/v1/apps/" + url.PathEscape(slug) + "/bindings/release-policy-with-queues?scope=" + url.QueryEscape(scope)
	err := c.do(ctx, http.MethodPut, path, request, &result)
	return result, err
}

const QueueEvidenceConsumerReadiness = "consumer_readiness"

// Queue requirements are explicit policy choices, never connectivity proofs.
// Keys select binding names within the policy's deployment scope.
func ValidateQueueReleaseRequirements(requirements map[string]string) error {
	if len(requirements) > QueueReleaseRequirementsMax {
		return fmt.Errorf("at most %d queue requirements are allowed", QueueReleaseRequirementsMax)
	}
	for name, evidence := range requirements {
		if !ValidQueueBindingName(name) || evidence != QueueEvidenceConsumerReadiness && evidence != QueueEvidenceHandlerCompletion {
			return fmt.Errorf("queue requirements must map valid binding names to consumer_readiness or handler_completion")
		}
	}
	return nil
}

func (c *Client) SetObjectStorageBindingReleasePolicy(ctx context.Context, slug, scope string, request SetObjectStorageBindingReleasePolicyRequest) (BindingReleasePolicy, error) {
	var result BindingReleasePolicy
	if request.ObjectStorageRequirements == nil {
		return result, fmt.Errorf("supply explicit object_storage_requirements (an empty object clears them)")
	}
	// Older servers must reject this route rather than accept a weaker policy.
	path := "/v1/apps/" + url.PathEscape(slug) + "/bindings/release-policy-with-object-storage?scope=" + url.QueryEscape(scope)
	err := c.do(ctx, http.MethodPut, path, request, &result)
	return result, err
}

const ObjectStorageRequirementsMax = 32

// Requirements select managed environment prefixes in the release policy's scope.
// Read accepts the legacy listing canary; read_write requires byte transfer too.
func ValidateObjectStorageRequirements(requirements map[string]string) error {
	if len(requirements) > ObjectStorageRequirementsMax {
		return fmt.Errorf("at most %d object-storage requirements are allowed", ObjectStorageRequirementsMax)
	}
	for prefix, mode := range requirements {
		if !ValidObjectStorageBindingPrefix(prefix) || mode != "read" && mode != "write" && mode != "read_write" {
			return fmt.Errorf("object-storage requirements must map valid environment prefixes to read, write or read_write")
		}
	}
	return nil
}
