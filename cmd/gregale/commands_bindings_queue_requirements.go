package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

type queueRequirementsFlag map[string]string

func (f *queueRequirementsFlag) String() string {
	parts := make([]string, 0, len(*f))
	for name, mode := range *f {
		parts = append(parts, name+"="+mode)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func (f *queueRequirementsFlag) Set(value string) error {
	name, mode, ok := strings.Cut(value, "=")
	if !ok || api.ValidateQueueReleaseRequirements(map[string]string{name: mode}) != nil {
		return fmt.Errorf("use NAME=consumer_readiness for an explicit push queue readiness requirement")
	}
	if *f == nil {
		*f = queueRequirementsFlag{}
	}
	if _, exists := (*f)[name]; exists {
		return fmt.Errorf("queue requirement %s was supplied more than once", name)
	}
	(*f)[name] = mode
	return api.ValidateQueueReleaseRequirements(*f)
}
