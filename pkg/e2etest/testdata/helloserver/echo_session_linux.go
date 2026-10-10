//go:build linux

package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

// runEchoSession is the interactive app-task fixture (ADR-958): it reports
// whether stdin is a terminal and its size, then echoes lines. "size"
// reprints the size, "exit" exits 7.
func runEchoSession() {
	fmt.Printf("tty:%v size:%s\n", stdinIsTerminal(), terminalSize())
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		switch line := strings.TrimSpace(scanner.Text()); line {
		case "exit":
			os.Exit(7)
		case "size":
			fmt.Printf("size:%s\n", terminalSize())
		default:
			fmt.Printf("got:%s\n", line)
		}
	}
}

func stdinIsTerminal() bool {
	var termios syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, 0, syscall.TCGETS, uintptr(unsafe.Pointer(&termios)))
	return errno == 0
}

func terminalSize() string {
	var size struct{ Row, Col, X, Y uint16 }
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, 0, syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&size))); errno != 0 {
		return "none"
	}
	return fmt.Sprintf("%dx%d", size.Row, size.Col)
}
