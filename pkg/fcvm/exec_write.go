package fcvm

import "syscall"

// writeExecutable runs write, which creates, fills and closes a file vmmd will
// exec later, while no other goroutine of this process can fork.
//
// A child forked while the file is open for writing inherits that descriptor
// and keeps it until the child itself execs, close-on-exec or not. Exec'ing the
// file in that window fails with ETXTBSY (golang/go#22315). On a 16-thread
// host, 30 concurrent cold boots lost one to it: "prepare jail device tree:
// nsenter: failed to execute /faas-mount-helper: Text file busy". Every fork
// takes syscall.ForkLock for writing, so holding it for reading while the
// descriptor exists means no child can ever hold one.
//
// write must not start a process itself: that fork would wait for this lock.
func writeExecutable(write func() error) error {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	return write()
}
