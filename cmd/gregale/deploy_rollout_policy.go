package main

import "errors"

// resolveDeployRollbackOn5xx applies the deploy-level rollout policy. Safe
// deploys always include first-wake 5xx rollback; an explicit attempt to turn
// that protection off is rejected instead of silently weakening --safe.
func resolveDeployRollbackOn5xx(safe, explicit, requested bool) (*bool, error) {
	if safe {
		if explicit && !requested {
			return nil, errors.New("--safe requires first-wake 5xx rollback; omit --rollback-on-5xx=false")
		}
		enabled := true
		return &enabled, nil
	}
	if !explicit {
		return nil, nil
	}
	enabled := requested
	return &enabled, nil
}
