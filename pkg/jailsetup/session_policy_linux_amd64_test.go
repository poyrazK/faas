//go:build linux && amd64

package jailsetup

// adr: 783
import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

func TestHostHelperSessionPolicyKernelInheritance(t *testing.T) {
	mode := os.Getenv("GREGALE_SESSION_POLICY_TEST")
	if mode == "" {
		for _, value := range []string{"control", "restricted"} {
			cmd := exec.Command(os.Args[0], "-test.run=^TestHostHelperSessionPolicyKernelInheritance$")
			cmd.Env = append(os.Environ(), "GREGALE_SESSION_POLICY_TEST="+value)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", value, err, out)
			}
		}
		return
	}
	if unix.Getpgrp() == os.Getpid() {
		t.Fatal("diagnostic must start outside its own process group")
	}
	if mode == "control" {
		if _, err := unix.Setsid(); err != nil {
			t.Fatal("unrestricted control cannot create a session", err)
		}
		return
	}
	if mode == "after-exec" {
		if _, err := unix.Setsid(); !errors.Is(err, unix.EPERM) {
			t.Fatal("exec descendant escaped original session", err)
		}
		return
	}
	ready, run := make(chan struct{}, 4), make(chan struct{})
	var joined sync.WaitGroup
	for range 4 {
		joined.Add(1)
		go func() {
			defer joined.Done()
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			ready <- struct{}{}
			<-run
			if _, err := unix.Setsid(); !errors.Is(err, unix.EPERM) {
				t.Error("existing thread escaped session policy", err)
			}
		}()
	}
	for range 4 {
		<-ready
	}
	if err := installHostHelperSessionPolicy(); err != nil {
		t.Fatal(err)
	}
	close(run)
	joined.Wait()
	if _, err := unix.Setsid(); !errors.Is(err, unix.EPERM) {
		t.Fatal("policy installer escaped original session", err)
	}
	_, _, errno := unix.RawSyscall(unix.SYS_GETPID|0x40000000, 0, 0, 0)
	if errno != unix.EPERM {
		t.Fatal("x32 ABI bypass accepted", errno)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestHostHelperSessionPolicyKernelInheritance$")
	cmd.Env = append(os.Environ(), "GREGALE_SESSION_POLICY_TEST=after-exec")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fork/exec inheritance: %v\n%s", err, out)
	}
}

func evaluateSessionFilter(arch, number uint32) (uint32, error) {
	var accumulator uint32
	for i := 0; i < len(hostHelperSessionFilter()); i++ {
		instruction := hostHelperSessionFilter()[i]
		switch instruction.Code {
		case unix.BPF_LD | unix.BPF_W | unix.BPF_ABS:
			if instruction.K == 4 {
				accumulator = arch
			} else if instruction.K == 0 {
				accumulator = number
			} else {
				return 0, fmt.Errorf("unexpected BPF offset %d", instruction.K)
			}
		case unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, unix.BPF_JMP | unix.BPF_JGE | unix.BPF_K:
			match := accumulator == instruction.K
			if instruction.Code == unix.BPF_JMP|unix.BPF_JGE|unix.BPF_K {
				match = accumulator >= instruction.K
			}
			if match {
				i += int(instruction.Jt)
			} else {
				i += int(instruction.Jf)
			}
		case unix.BPF_RET | unix.BPF_K:
			return instruction.K, nil
		default:
			return 0, errors.New("unexpected BPF instruction")
		}
	}
	return 0, errors.New("BPF program did not return")
}

func TestHostHelperSessionPolicyRejectsCompatABIs(t *testing.T) {
	for _, value := range []struct{ arch, number uint32 }{{unix.AUDIT_ARCH_I386, 66}, {unix.AUDIT_ARCH_AARCH64, 157}, {unix.AUDIT_ARCH_X86_64, unix.SYS_SETSID}, {unix.AUDIT_ARCH_X86_64, unix.SYS_SETSID | 0x40000000}, {unix.AUDIT_ARCH_X86_64, 521}, {unix.AUDIT_ARCH_X86_64, 0xffffffff}} {
		got, err := evaluateSessionFilter(value.arch, value.number)
		if err != nil || got != unix.SECCOMP_RET_ERRNO|uint32(unix.EPERM) {
			t.Fatal("session/compat bypass", value, got, err)
		}
	}
	for _, number := range []uint32{unix.SYS_GETPID, unix.SYS_CLONE, unix.SYS_EXECVE, unix.SYS_SETPGID, unix.SYS_MOUNT, unix.SYS_SETNS} {
		got, err := evaluateSessionFilter(unix.AUDIT_ARCH_X86_64, number)
		if err != nil || got != unix.SECCOMP_RET_ALLOW {
			t.Fatal("native utility operation denied", number, got, err)
		}
	}
}
