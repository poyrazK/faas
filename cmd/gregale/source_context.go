package main

import "os"

// Select the source before resolving any defaults. An explicit --path must
// never combine an app from that source with the caller's environment.
func deployContextDirectory(path string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if path == "" {
		return cwd, nil
	}
	return resolveDeploySourceDir(cwd, path)
}
