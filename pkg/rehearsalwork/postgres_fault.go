package rehearsalwork

import "errors"

func (e Evidence) validatePostgresFault() error {
	spec, f := e.Plan.PostgresFault, e.PostgresFault
	if spec == nil {
		if f != nil {
			return errors.New("unexpected PostgreSQL fault evidence")
		}
		return nil
	}
	if f == nil || f.Validate() != nil || f.Spec != *spec || f.RunID != e.RunID || f.PlanSHA256 != e.PlanSHA256 || len(e.Jobs) != 1 {
		return errors.New("PostgreSQL fault lacks complete activation and recovery evidence")
	}
	j := e.Jobs[0]
	if j.ID != f.InvocationID || j.Workload != spec.Workload || j.AppID != f.AppID || j.DeploymentID != f.DeploymentID || len(j.Attempts) == 0 || j.Attempts[0].InstanceID != f.InstanceID {
		return errors.New("PostgreSQL fault differs from its first managed dispatch")
	}
	for _, a := range j.Attempts {
		if a.InstanceID != f.InstanceID {
			return errors.New("PostgreSQL fault recovery cannot replace its original worker")
		}
	}
	for _, b := range e.Bindings {
		if b.PostgresResourceID != f.ResourceID {
			return errors.New("PostgreSQL fault resource binding differs")
		}
	}
	return nil
}
