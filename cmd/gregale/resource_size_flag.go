package main

import (
	"errors"
	"flag"
)

// The command-local RAM/CPU size is --size. Its older spelling, --profile
// after the command, keeps working but is deprecated: before the command,
// --profile selects a connection (extractConnectionProfile), and one flag
// with two meanings is hard to read in scripts and docs.

var errResourceSizeSpellings = errors.New("use --size or --profile, not both")

// resourceSizeFlag registers --size and its deprecated --profile spelling.
// Both write target; giving both is a usage error, even with equal values.
func resourceSizeFlag(fs *flag.FlagSet, target *string, usage string) {
	var seen string
	fs.Var(&resourceSizeValue{target: target, name: "size", seen: &seen}, "size", usage)
	fs.Var(&resourceSizeValue{target: target, name: "profile", seen: &seen}, "profile", "deprecated: use --size")
}

// resourceSizeSet reports whether either spelling appeared on the command line.
func resourceSizeSet(explicit map[string]bool) bool {
	return explicit["size"] || explicit["profile"]
}

// resourceSizeSpelling names the size flag the way the user typed it.
func resourceSizeSpelling(explicit map[string]bool) string {
	if explicit["profile"] {
		return "--profile"
	}
	return "--size"
}

type resourceSizeValue struct {
	target *string
	name   string
	seen   *string
}

func (v *resourceSizeValue) String() string {
	if v == nil || v.target == nil {
		return ""
	}
	return *v.target
}

func (v *resourceSizeValue) Set(value string) error {
	if *v.seen != "" && *v.seen != v.name {
		return errResourceSizeSpellings
	}
	*v.seen = v.name
	*v.target = value
	return nil
}
