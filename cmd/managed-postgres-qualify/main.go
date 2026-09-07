// Command managed-postgres-qualify runs an isolated provider qualification.
// It is intentionally a separate binary so a live run cannot be triggered by
// the apid process or by customer traffic.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/neon"
)

type qualificationOutput struct {
	BackendID          string                              `json:"backend_id"`
	BackendFingerprint string                              `json:"backend_fingerprint"`
	Spec               managedpostgres.Spec                `json:"spec"`
	Report             managedpostgres.QualificationReport `json:"report"`
}

func main() {
	os.Exit(run(os.Getenv, os.Stdout, os.Stderr))
}

func run(getenv func(string) string, output, errorOutput io.Writer) int {
	if !isLiveQualificationEnabled(getenv) {
		_, _ = fmt.Fprintln(errorOutput, "managed postgres qualification requires FAAS_ENVIRONMENT=staging and FAAS_MANAGED_POSTGRES_QUALIFY_LIVE=true")
		return 2
	}
	registry, err := managedpostgres.Load(getenv, map[string]managedpostgres.Factory{"neon": neon.New})
	if err != nil || registry == nil {
		_, _ = fmt.Fprintln(errorOutput, "managed postgres qualification configuration is unavailable")
		return 2
	}
	resourceID := strings.TrimSpace(getenv("FAAS_MANAGED_POSTGRES_QUALIFY_RESOURCE_ID"))
	if resourceID == "" || len(resourceID) > 255 {
		_, _ = fmt.Fprintln(errorOutput, "FAAS_MANAGED_POSTGRES_QUALIFY_RESOURCE_ID is required and must be at most 255 characters")
		return 2
	}
	region := strings.TrimSpace(getenv("FAAS_MANAGED_POSTGRES_QUALIFY_REGION"))
	if region == "" {
		region = registry.DefaultRegion
	}
	backend, err := registry.Default(region)
	if err != nil {
		_, _ = fmt.Fprintln(errorOutput, "qualification region has no configured backend")
		return 2
	}
	spec, err := qualificationSpec(backend, region)
	if err != nil {
		_, _ = fmt.Fprintln(errorOutput, "configured backend cannot produce a qualification spec")
		return 2
	}
	timeout := 10 * time.Minute
	if value := strings.TrimSpace(getenv("FAAS_MANAGED_POSTGRES_QUALIFY_TIMEOUT")); value != "" {
		timeout, err = time.ParseDuration(value)
		if err != nil || timeout <= 0 {
			_, _ = fmt.Fprintln(errorOutput, "FAAS_MANAGED_POSTGRES_QUALIFY_TIMEOUT must be a positive duration")
			return 2
		}
	}
	report, qualificationErr := managedpostgres.QualifyProvider(context.Background(), backend.Provider, managedpostgres.QualificationOptions{
		ProviderName: backend.Driver,
		ResourceID:   resourceID,
		Spec:         spec,
		Timeout:      timeout,
		Mutating:     true,
	})
	result := qualificationOutput{BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Spec: spec, Report: report}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		_, _ = fmt.Fprintln(errorOutput, "cannot write qualification report")
		return 1
	}
	if qualificationErr != nil {
		_, _ = fmt.Fprintln(errorOutput, "managed postgres provider qualification failed")
		return 1
	}
	return 0
}

func isLiveQualificationEnabled(getenv func(string) string) bool {
	if getenv == nil || !strings.EqualFold(strings.TrimSpace(getenv(managedpostgres.EnvironmentEnv)), managedpostgres.QualificationStagingEnvironment) {
		return false
	}
	approved, err := strconv.ParseBool(strings.TrimSpace(getenv("FAAS_MANAGED_POSTGRES_QUALIFY_LIVE")))
	return err == nil && approved
}

func qualificationSpec(backend managedpostgres.Backend, region string) (managedpostgres.Spec, error) {
	capabilities := backend.Capabilities
	if err := capabilities.Validate(); err != nil {
		return managedpostgres.Spec{}, err
	}
	if !capabilities.ScaleToZero {
		return managedpostgres.Spec{}, managedpostgres.ErrUnsupported
	}
	major := capabilities.PostgresMajors[0]
	for _, candidate := range capabilities.PostgresMajors[1:] {
		if candidate > major {
			major = candidate
		}
	}
	class := managedpostgres.ClassDevelopment
	if !containsServiceClass(capabilities.ServiceClasses, class) {
		if len(capabilities.ServiceClasses) == 0 {
			return managedpostgres.Spec{}, errors.New("no service class")
		}
		class = capabilities.ServiceClasses[0]
	}
	availability := managedpostgres.AvailabilitySingleZone
	if !containsAvailability(capabilities.Availability, availability) {
		if len(capabilities.Availability) == 0 {
			return managedpostgres.Spec{}, errors.New("no availability")
		}
		availability = capabilities.Availability[0]
	}
	storage := int64(1 << 30)
	if capabilities.MaxStorageBytes > 0 && capabilities.MaxStorageBytes < storage {
		storage = capabilities.MaxStorageBytes
	}
	if storage <= 0 {
		return managedpostgres.Spec{}, errors.New("no storage capacity")
	}
	restoreWindow := int64(0)
	if capabilities.PointInTimeRestore {
		restoreWindow = int64(24 * time.Hour / time.Second)
		if capabilities.MaxRestoreWindowSeconds > 0 && capabilities.MaxRestoreWindowSeconds < restoreWindow {
			restoreWindow = capabilities.MaxRestoreWindowSeconds
		}
	}
	spec := managedpostgres.Spec{
		Region:               region,
		PostgresMajor:        major,
		Class:                class,
		Availability:         availability,
		ScaleToZero:          capabilities.ScaleToZero,
		StorageLimitBytes:    storage,
		RestoreWindowSeconds: restoreWindow,
	}
	if err := capabilities.Supports(spec); err != nil {
		return managedpostgres.Spec{}, err
	}
	return spec, nil
}

func containsServiceClass(values []managedpostgres.ServiceClass, wanted managedpostgres.ServiceClass) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func containsAvailability(values []managedpostgres.Availability, wanted managedpostgres.Availability) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
