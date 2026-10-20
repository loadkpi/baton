// Package terminal answers whether a file is an interactive terminal.
package terminal

import (
	"os"
	"syscall"
	"unsafe"
)

// IsTerminal reports whether f is a terminal. A character device that is not a
// terminal, such as /dev/null, is not one.
func IsTerminal(f *os.File) bool {
	conn, err := f.SyscallConn()
	if err != nil {
		return false
	}
	var termios syscall.Termios
	var errno syscall.Errno
	if err := conn.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCGETS, uintptr(unsafe.Pointer(&termios)))
	}); err != nil {
		return false
	}
	return errno == 0
}
