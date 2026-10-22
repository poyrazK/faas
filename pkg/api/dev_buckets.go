package api

import (
	"fmt"
	"regexp"
	"strings"
)

// DevBucketRequest explicitly provisions private storage in the developer app.
// Omission preserves existing resources; it never detaches or deletes a bucket.
type DevBucketRequest struct {
	Name       string                         `json:"name" yaml:"name"`
	Region     string                         `json:"region,omitempty" yaml:"region,omitempty"`
	Permission string                         `json:"permission,omitempty" yaml:"permission,omitempty"`
	Prefix     string                         `json:"prefix,omitempty" yaml:"prefix,omitempty"`
	Triggers   *[]ObjectStorageComputeTrigger `json:"triggers,omitempty" yaml:"triggers,omitempty"`
}

// DevBucketResponse contains only safe metadata for an active automatic binding.
type DevBucketResponse struct {
	Bucket  ObjectBucket                `json:"bucket"`
	Binding ObjectStorageComputeBinding `json:"binding"`
}

// DefaultObjectStorageBindingPrefix is shared by explicit and developer bindings.
func DefaultObjectStorageBindingPrefix(name string) string {
	var b strings.Builder
	b.WriteString("GREGALE_S3_")
	for _, r := range strings.ToUpper(name) {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	prefix := strings.TrimRight(b.String(), "_")
	if len(prefix) > 48 {
		prefix = strings.TrimRight(prefix[:48], "_")
	}
	return prefix
}

// NormalizeDevBuckets validates the whole batch before any remote mutation.
// A separate slice keeps validation from mutating the caller's manifest.
func NormalizeDevBuckets(requests []DevBucketRequest) ([]DevBucketRequest, error) {
	var out []DevBucketRequest
	names, prefixes := map[string]bool{}, map[string]bool{}
	for _, request := range requests {
		if !regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`).MatchString(request.Name) {
			return nil, fmt.Errorf("bucket name must be 1–63 lowercase letters, digits or hyphens, starting with a letter")
		}
		if request.Region != "" && ValidateScope(request.Region) != nil {
			return nil, fmt.Errorf("invalid bucket region")
		}
		if request.Permission == "" {
			request.Permission = ObjectBucketPermissionReadWrite
		}
		if request.Permission != ObjectBucketPermissionRead && request.Permission != ObjectBucketPermissionWrite && request.Permission != ObjectBucketPermissionReadWrite {
			return nil, fmt.Errorf("invalid bucket permission")
		}
		if request.Prefix == "" {
			request.Prefix = DefaultObjectStorageBindingPrefix(request.Name)
		}
		if request.Triggers != nil {
			triggers, err := NormalizeObjectStorageComputeTriggers(*request.Triggers)
			if err != nil {
				return nil, err
			}
			request.Triggers = &triggers
		}
		if !ValidObjectStorageBindingPrefix(request.Prefix) || names[request.Name] || prefixes[request.Prefix] {
			return nil, fmt.Errorf("bucket names and binding prefixes must be valid and unique")
		}
		names[request.Name], prefixes[request.Prefix] = true, true
		out = append(out, request)
	}
	return out, nil
}
