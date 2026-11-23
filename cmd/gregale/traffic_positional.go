package main

import "errors"

// leadingDeploymentID folds a peeled leading positional into --deployment
// when it is a deployment UUID, which no app slug is. `release traffic promote
// <id> --app shop` said the positional and --app "name different apps",
// though `release timeline <id> --app shop` takes the id that way (prod hunt
// r32). A `vN` stays a slug because an app may be named that. It returns the
// slug still to merge into --app. Call it after the flags are parsed.
func leadingDeploymentID(slug string, deployment *string) (string, error) {
	if !isUUIDArgument(slug) {
		return slug, nil
	}
	if *deployment != "" && *deployment != slug {
		return "", errors.New("pass the deployment once: the positional id and --deployment name different deployments")
	}
	*deployment = slug
	return "", nil
}
