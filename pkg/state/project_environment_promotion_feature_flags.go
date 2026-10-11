package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
)

// ProjectEnvironmentPromotionFeatureFlags exposes identities and activation
// receipts without returning private flag targeting configuration.
type ProjectEnvironmentPromotionFeatureFlags struct {
	SourceHash         string
	PreviousTargetHash string
	TargetVersion      int64
	RollbackVersion    int64
}

type ProjectEnvironmentPromotionFeatureFlagStore interface {
	ProjectEnvironmentPromotionFeatureFlags(context.Context, string, string) (ProjectEnvironmentPromotionFeatureFlags, error)
}

type promotionFeatureFlags struct {
	ProjectEnvironmentPromotionFeatureFlags
	Source         projectCloneFeatureFlags
	PreviousTarget projectCloneFeatureFlags
}

// FeatureFlagsPromotionHash includes the environment lifetime even when no
// flags have been published. Approval must also fence the first flag update.
func FeatureFlagsPromotionHash(version FeatureFlagVersion) (string, error) {
	snapshot, err := captureCloneFeatureFlags(version)
	if err != nil {
		return "", err
	}
	return cloneFeatureFlagsHash(&snapshot), nil
}

func capturePromotionFeatureFlags(promotion ProjectEnvironmentPromotion, source, target FeatureFlagVersion) (promotionFeatureFlags, error) {
	var captured promotionFeatureFlags
	var err error
	captured.Source, err = captureCloneFeatureFlags(source)
	if err != nil {
		return captured, err
	}
	captured.PreviousTarget, err = captureCloneFeatureFlags(target)
	if err != nil {
		return captured, err
	}
	if source.EnvironmentID == target.EnvironmentID {
		return captured, ErrConflict
	}
	captured.SourceHash = cloneFeatureFlagsHash(&captured.Source)
	captured.PreviousTargetHash = cloneFeatureFlagsHash(&captured.PreviousTarget)
	if (promotion.SourceFeatureFlagsHash != "" && promotion.SourceFeatureFlagsHash != captured.SourceHash) ||
		(promotion.PreviousTargetFeatureFlagsHash != "" && promotion.PreviousTargetFeatureFlagsHash != captured.PreviousTargetHash) {
		return captured, ErrConflict
	}
	return captured, nil
}

func (captured promotionFeatureFlags) authenticate() error {
	source, err := normalizeCloneFeatureFlags(captured.Source)
	if err != nil || cloneFeatureFlagsHash(&source) != captured.SourceHash {
		return ErrConflict
	}
	target, err := normalizeCloneFeatureFlags(captured.PreviousTarget)
	if err != nil || cloneFeatureFlagsHash(&target) != captured.PreviousTargetHash || source.SourceEnvironmentID == target.SourceEnvironmentID {
		return ErrConflict
	}
	if captured.TargetVersion < 0 || captured.TargetVersion > api.FlagsMaxConfigVersion ||
		captured.RollbackVersion < 0 || captured.RollbackVersion > api.FlagsMaxConfigVersion ||
		(captured.TargetVersion != 0 && captured.TargetVersion != target.SourceVersion+1) ||
		(captured.RollbackVersion != 0 && (captured.TargetVersion == 0 || captured.RollbackVersion != captured.TargetVersion+1)) {
		return ErrConflict
	}
	return nil
}

func preparePromotionFeatureFlagActivation(captured promotionFeatureFlags, source, target FeatureFlagVersion, rollback, completed bool) (FeatureFlagVersion, error) {
	if err := captured.authenticate(); err != nil {
		return FeatureFlagVersion{}, err
	}
	if (!rollback && ((!completed && captured.TargetVersion != 0) || captured.RollbackVersion != 0)) ||
		(rollback && !completed && captured.RollbackVersion != 0) {
		return FeatureFlagVersion{}, ErrConflict
	}
	if target.EnvironmentID != captured.PreviousTarget.SourceEnvironmentID {
		return FeatureFlagVersion{}, ErrConflict
	}
	expectedVersion, expectedConfig, expectedActor := captured.PreviousTarget.SourceVersion, captured.PreviousTarget.Config, ""
	expectedRestoredFrom := int64(0)
	if rollback || completed {
		expectedVersion, expectedConfig, expectedActor = captured.TargetVersion, captured.Source.Config, "environment-promotion"
		if expectedVersion == 0 {
			return FeatureFlagVersion{}, ErrConflict
		}
	}
	if rollback && completed {
		expectedVersion, expectedConfig, expectedActor = captured.RollbackVersion, captured.PreviousTarget.Config, "environment-promotion-rollback"
		expectedRestoredFrom = captured.PreviousTarget.SourceVersion
		if expectedVersion == 0 {
			return FeatureFlagVersion{}, ErrConflict
		}
	}
	actual, err := captureCloneFeatureFlags(target)
	if err != nil || target.Version != expectedVersion || !samePromotionFlagConfig(actual.Config, expectedConfig) ||
		(expectedActor != "" && (target.Actor != expectedActor || target.RestoredFrom != expectedRestoredFrom)) {
		return FeatureFlagVersion{}, ErrConflict
	}
	if completed {
		return FeatureFlagVersion{}, nil
	}
	config, actor, restoredFrom := captured.Source.Config, "environment-promotion", int64(0)
	if rollback {
		config, actor, restoredFrom = captured.PreviousTarget.Config, "environment-promotion-rollback", captured.PreviousTarget.SourceVersion
	} else {
		hash, err := FeatureFlagsPromotionHash(source)
		if err != nil || hash != captured.SourceHash {
			return FeatureFlagVersion{}, ErrConflict
		}
		seeds := make(map[string]string, len(target.Flags))
		for _, flag := range target.Flags {
			seeds[flag.Key] = flag.Seed
		}
		for _, flag := range config.Flags {
			if seed := seeds[flag.Key]; seed != "" && seed != flag.Seed {
				return FeatureFlagVersion{}, ErrConflict
			}
		}
	}
	if target.Version >= api.FlagsMaxConfigVersion {
		return FeatureFlagVersion{}, ErrConflict
	}
	return cloneFeatureFlags(FeatureFlagVersion{Bundle: flags.Bundle{EnvironmentID: target.EnvironmentID, Version: target.Version + 1, Config: config},
		Actor: actor, RestoredFrom: restoredFrom, CreatedAt: time.Now().UTC()}), nil
}

func samePromotionFlagConfig(left, right flags.Config) bool {
	a, err := json.Marshal(left)
	if err != nil {
		return false
	}
	b, err := json.Marshal(right)
	return err == nil && string(a) == string(b)
}
