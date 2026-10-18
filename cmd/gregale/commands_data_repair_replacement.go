package main

import "github.com/onebox-faas/faas/pkg/datarepair"

func validReplacementCLI(p datarepair.Plan, parent, digest, id string) bool {
	return p.ID == id && p.Scope.Validate() == nil && p.Digest != "" && p.Replacement != nil &&
		p.Replacement.ParentPlanID == parent && p.Replacement.ParentPlanDigest == digest && p.Replacement.Valid(p)
}
