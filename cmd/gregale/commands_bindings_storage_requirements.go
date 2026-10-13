package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

type storageRequirementsFlag map[string]string

func (f *storageRequirementsFlag) String() string {
	if f == nil {
		return ""
	}
	keys := make([]string, 0, len(*f))
	for key := range *f {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, key+"="+(*f)[key])
	}
	return strings.Join(values, ", ")
}

func (f *storageRequirementsFlag) Set(value string) error {
	prefix, mode, ok := strings.Cut(value, "=")
	if !ok || api.ValidateObjectStorageRequirements(map[string]string{prefix: mode}) != nil {
		return fmt.Errorf("use PREFIX=read, PREFIX=write or PREFIX=read_write")
	}
	if _, exists := (*f)[prefix]; exists {
		return fmt.Errorf("object-storage prefix %s was supplied more than once", prefix)
	}
	if *f == nil {
		*f = storageRequirementsFlag{}
	}
	(*f)[prefix] = mode
	return api.ValidateObjectStorageRequirements(*f)
}
