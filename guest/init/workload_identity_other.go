//go:build !linux

package main

func StampWorkloadIdentityEnv(env []string) []string { return env }
