package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var (
	ErrProjectEnvironmentCloneBindingCapture            = fmt.Errorf("clone binding catalogue is incomplete or not ready: %w", ErrConflict)
	ErrProjectEnvironmentCloneBindingCaptureUnavailable = fmt.Errorf("clone binding catalogue capture is unavailable: %w", ErrConflict)
	ErrProjectEnvironmentCloneResourcePublicationProof  = fmt.Errorf("isolated resource publication proof is unavailable: %w", ErrConflict)
)

// ProjectEnvironmentCloneBindings contains frozen resource definitions for a
// worker. It contains no passwords, access keys or encrypted credential material.
// It is account/project scoped and is not an API response type.
type ProjectEnvironmentCloneBindings struct {
	AppID       string `json:"app_id"`
	SourceScope string `json:"source_scope"`
	Hash        string `json:"hash"`
	ProjectEnvironmentCloneBindingDefinitions
}

type ProjectEnvironmentCloneBindingCaptureStore interface {
	ProjectEnvironmentCloneBindings(context.Context, string, string, string) ([]ProjectEnvironmentCloneBindings, error)
}

type ProjectEnvironmentCloneBindingDefinitions struct {
	Postgres []ProjectEnvironmentClonePostgresBinding `json:"postgres"`
	Buckets  []ProjectEnvironmentCloneObjectBucket    `json:"buckets"`
}

// The definition stores only desired configuration and source identities.
// Leases, retry state and provider authentication are not clone inputs.
type ProjectEnvironmentClonePostgresBinding struct {
	ID                   string `json:"id"`
	DatabaseID           string `json:"database_id"`
	DatabaseName         string `json:"database_name"`
	EnvironmentKey       string `json:"environment_key"`
	Access               string `json:"access"`
	CredentialRef        string `json:"credential_ref"`
	CredentialGeneration int64  `json:"credential_generation"`
	BackendID            string `json:"backend_id"`
	BackendFingerprint   string `json:"backend_fingerprint"`
	ProviderResourceID   string `json:"provider_resource_id"`
	Region               string `json:"region"`
	PostgresMajor        int    `json:"postgres_major"`
	ServiceClass         string `json:"service_class"`
	Availability         string `json:"availability"`
	ScaleToZero          bool   `json:"scale_to_zero"`
	StorageLimitBytes    int64  `json:"storage_limit_bytes"`
	RestoreWindowSeconds int64  `json:"restore_window_seconds"`
}

type ProjectEnvironmentCloneObjectBucket struct {
	ID                 string                                     `json:"id"`
	Name               string                                     `json:"name"`
	Region             string                                     `json:"region"`
	BackendID          string                                     `json:"backend_id"`
	BackendFingerprint string                                     `json:"backend_fingerprint"`
	PhysicalName       string                                     `json:"physical_name"`
	PublicRead         bool                                       `json:"public_read"`
	ServeAt            string                                     `json:"serve_at"`
	Credentials        []ProjectEnvironmentCloneObjectCredential  `json:"credentials"`
	AccessGrants       []ProjectEnvironmentCloneObjectAccessGrant `json:"access_grants"`
}

type ProjectEnvironmentCloneObjectCredential struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	Permission    string `json:"permission"`
	ManagedAppID  string `json:"managed_app_id"`
	ManagedScope  string `json:"managed_scope"`
	ManagedPrefix string `json:"managed_prefix"`
}

type ProjectEnvironmentCloneObjectAccessGrant struct {
	APIKeyID   string `json:"api_key_id"`
	Permission string `json:"permission"`
}

func normalizeCloneBindingDefinitions(appID, scope string, values projectCloneWorkloadValues, definitions ProjectEnvironmentCloneBindingDefinitions) (ProjectEnvironmentCloneBindingDefinitions, error) {
	definitions.Postgres = append([]ProjectEnvironmentClonePostgresBinding{}, definitions.Postgres...)
	definitions.Buckets = append([]ProjectEnvironmentCloneObjectBucket{}, definitions.Buckets...)
	sort.Slice(definitions.Postgres, func(i, j int) bool { return definitions.Postgres[i].ID < definitions.Postgres[j].ID })
	sort.Slice(definitions.Buckets, func(i, j int) bool { return definitions.Buckets[i].ID < definitions.Buckets[j].ID })
	covered := map[string]string{}
	databaseDefinitions := map[string]ProjectEnvironmentClonePostgresBinding{}
	keys := map[string]bool{}
	for i, binding := range definitions.Postgres {
		if !validClonePostgresBinding(binding) || (i > 0 && definitions.Postgres[i-1].ID == binding.ID) || keys[binding.EnvironmentKey] {
			return definitions, ErrProjectEnvironmentCloneBindingCapture
		}
		keys[binding.EnvironmentKey] = true
		if previous, exists := databaseDefinitions[binding.DatabaseID]; exists && !sameClonePostgresDatabase(previous, binding) {
			return definitions, ErrProjectEnvironmentCloneBindingCapture
		}
		databaseDefinitions[binding.DatabaseID] = binding
		matched := false
		for _, secret := range values.Secrets {
			if secret.ManagedPostgresBindingID != binding.ID {
				continue
			}
			if matched || secret.Key != binding.EnvironmentKey || secret.ManagedCredentialRef != binding.CredentialRef || secret.ManagedCredentialGeneration != binding.CredentialGeneration {
				return definitions, ErrProjectEnvironmentCloneBindingCapture
			}
			matched = true
			covered[secret.Key] = binding.ID
		}
		if !matched {
			return definitions, ErrProjectEnvironmentCloneBindingCapture
		}
	}
	credentials := map[string]bool{}
	for i := range definitions.Buckets {
		bucket := &definitions.Buckets[i]
		if bucket.ID == "" || !cloneBindingNameRE.MatchString(bucket.Name) || bucket.Region == "" || bucket.BackendID == "" || !cloneBindingFingerprintRE.MatchString(bucket.BackendFingerprint) || bucket.PhysicalName == "" ||
			(i > 0 && definitions.Buckets[i-1].ID == bucket.ID) || !validCloneBucketPublicPolicy(bucket.PublicRead, bucket.ServeAt) {
			return definitions, ErrProjectEnvironmentCloneBindingCapture
		}
		bucket.Credentials = append([]ProjectEnvironmentCloneObjectCredential{}, bucket.Credentials...)
		bucket.AccessGrants = append([]ProjectEnvironmentCloneObjectAccessGrant{}, bucket.AccessGrants...)
		sort.Slice(bucket.Credentials, func(i, j int) bool { return bucket.Credentials[i].ID < bucket.Credentials[j].ID })
		sort.Slice(bucket.AccessGrants, func(i, j int) bool { return bucket.AccessGrants[i].APIKeyID < bucket.AccessGrants[j].APIKeyID })
		for _, credential := range bucket.Credentials {
			if credential.ID == "" || credentials[credential.ID] || len(credential.Label) < 1 || len(credential.Label) > 64 || !validObjectBucketPermission(credential.Permission) {
				return definitions, ErrProjectEnvironmentCloneBindingCapture
			}
			credentials[credential.ID] = true
			if credential.ManagedAppID == "" {
				if credential.ManagedScope != "" || credential.ManagedPrefix != "" {
					return definitions, ErrProjectEnvironmentCloneBindingCapture
				}
				continue
			}
			if credential.ManagedAppID != appID || credential.ManagedScope != scope || !cloneBindingPrefixRE.MatchString(credential.ManagedPrefix) {
				return definitions, ErrProjectEnvironmentCloneBindingCapture
			}
			want := map[string]bool{}
			for _, suffix := range []string{"_ENDPOINT", "_REGION", "_BUCKET", "_ACCESS_KEY_ID", "_SECRET_ACCESS_KEY", "_ADDRESSING_STYLE"} {
				want[credential.ManagedPrefix+suffix] = true
			}
			for _, secret := range values.Secrets {
				if secret.ManagedObjectStorageCredentialID != credential.ID {
					continue
				}
				if !want[secret.Key] || covered[secret.Key] != "" {
					return definitions, ErrProjectEnvironmentCloneBindingCapture
				}
				delete(want, secret.Key)
				covered[secret.Key] = credential.ID
			}
			if len(want) != 0 {
				return definitions, ErrProjectEnvironmentCloneBindingCapture
			}
		}
		for i, grant := range bucket.AccessGrants {
			if grant.APIKeyID == "" || !validObjectBucketPermission(grant.Permission) || (i > 0 && bucket.AccessGrants[i-1].APIKeyID == grant.APIKeyID) {
				return definitions, ErrProjectEnvironmentCloneBindingCapture
			}
		}
	}
	for _, secret := range values.Secrets {
		if id := cloneSecretManagedID(secret); id != "" && covered[secret.Key] != id {
			return definitions, ErrProjectEnvironmentCloneBindingCapture
		}
	}
	return definitions, nil
}

var (
	cloneBindingNameRE           = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	cloneBindingPublicPathRE     = regexp.MustCompile(`^/[A-Za-z0-9][A-Za-z0-9._~/-]*$`)
	cloneBindingFingerprintRE    = regexp.MustCompile(`^[a-f0-9]{64}$`)
	cloneBindingPrefixRE         = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,47}$`)
	cloneBindingEnvironmentKeyRE = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,126}$`)
)

func validClonePostgresBinding(b ProjectEnvironmentClonePostgresBinding) bool {
	return b.ID != "" && b.DatabaseID != "" && cloneBindingNameRE.MatchString(b.DatabaseName) && cloneBindingEnvironmentKeyRE.MatchString(b.EnvironmentKey) &&
		(b.Access == "read_write" || b.Access == "read_only") && b.CredentialRef != "" && b.CredentialGeneration > 0 && cloneBindingNameRE.MatchString(b.BackendID) &&
		cloneBindingFingerprintRE.MatchString(b.BackendFingerprint) && b.ProviderResourceID != "" && cloneBindingNameRE.MatchString(b.Region) && b.PostgresMajor >= 12 && b.PostgresMajor <= 99 &&
		(b.ServiceClass == "development" || b.ServiceClass == "burstable" || b.ServiceClass == "production") && (b.Availability == "single_zone" || b.Availability == "high_availability") &&
		b.StorageLimitBytes >= 0 && b.RestoreWindowSeconds > 0
}

func validCloneBucketPublicPolicy(public bool, path string) bool {
	if !public {
		return path == ""
	}
	if !cloneBindingPublicPathRE.MatchString(path) || strings.HasSuffix(path, "/") || strings.Contains(path, "//") {
		return false
	}
	for _, segment := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func sameClonePostgresDatabase(a, b ProjectEnvironmentClonePostgresBinding) bool {
	a.ID, a.EnvironmentKey, a.Access, a.CredentialRef, a.CredentialGeneration = "", "", "", "", 0
	b.ID, b.EnvironmentKey, b.Access, b.CredentialRef, b.CredentialGeneration = "", "", "", "", 0
	return a == b
}

func cloneBindingDefinitionsHash(definitions ProjectEnvironmentCloneBindingDefinitions) (string, error) {
	raw, err := json.Marshal(definitions)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), nil
}

func cloneBindingViews(records []projectCloneWorkloadRecord) ([]ProjectEnvironmentCloneBindings, error) {
	views := make([]ProjectEnvironmentCloneBindings, 0, len(records))
	for _, record := range records {
		copy, err := copyCloneWorkloadRecord(record)
		if err != nil {
			return nil, err
		}
		if copy.snapshot.Bindings == nil || copy.SourceBindingsHash == "" {
			return nil, ErrProjectEnvironmentCloneBindingCaptureUnavailable
		}
		views = append(views, ProjectEnvironmentCloneBindings{AppID: copy.AppID, SourceScope: copy.SourceScope, Hash: copy.SourceBindingsHash, ProjectEnvironmentCloneBindingDefinitions: *copy.snapshot.Bindings})
	}
	sort.Slice(views, func(i, j int) bool { return views[i].AppID < views[j].AppID })
	return views, nil
}
