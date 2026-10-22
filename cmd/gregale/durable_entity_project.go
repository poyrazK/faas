package main

import "github.com/onebox-faas/faas/pkg/durableentity/project"

func doctorCheckDurableEntities(path string) (doctorCheck, bool) {
	present, err := project.Check(path)
	if !present {
		return doctorCheck{}, false
	}
	check := doctorCheck{Name: "durable-entities", Status: "ok", Hint: "Declared source artifacts are present; runtime behavior and preview admission remain unverified."}
	if err != nil {
		check.Status = "error"
		check.Code = "durable_entity_project_invalid"
		check.Hint = err.Error()
		check.Fix = "Follow the durable-entity starter README: package the current SDK, install dependencies, prepare the validator, then run gregale doctor."
	}
	return check, true
}
