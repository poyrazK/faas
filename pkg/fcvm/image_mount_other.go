//go:build !linux

package fcvm

import "os/exec"

func bindFileMount(source, target string) ([]byte, error) {
	return exec.Command("mount", "--bind", source, target).CombinedOutput()
}

func makeFileMountReadOnly(target string) ([]byte, error) {
	return exec.Command("mount", "-o", "remount,bind,ro", target).CombinedOutput()
}
