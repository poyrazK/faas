package main

import (
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

type devBucketNames []string

func (names *devBucketNames) String() string { return strings.Join(*names, ",") }

func (names *devBucketNames) Set(name string) error {
	if _, err := api.NormalizeDevBuckets([]api.DevBucketRequest{{Name: name}}); err != nil {
		return err
	}
	*names = append(*names, name)
	return nil
}

func resolveDevBuckets(manifest *gregalemanifest.Manifest, names devBucketNames, explicit bool) ([]api.DevBucketRequest, error) {
	var requests []api.DevBucketRequest
	if explicit {
		for _, name := range names {
			requests = append(requests, api.DevBucketRequest{Name: name})
		}
	} else if manifest != nil && manifest.Dev != nil {
		requests = manifest.Dev.Buckets
	}
	return api.NormalizeDevBuckets(requests)
}

// A manifest edit takes effect before the next source sync. Explicit flags keep
// their invocation-wide precedence; omission still preserves remote resources.
func resolveDevBucketsForSync(sourceDir string, names devBucketNames, explicit bool) ([]api.DevBucketRequest, error) {
	if explicit {
		return resolveDevBuckets(nil, names, true)
	}
	manifest, err := loadDevManifest(sourceDir)
	if err != nil {
		return nil, err
	}
	return resolveDevBuckets(manifest, nil, false)
}

func appendDevBucketArgs(args []string, names devBucketNames) []string {
	for _, name := range names {
		args = append(args, "--bucket", name)
	}
	return args
}
