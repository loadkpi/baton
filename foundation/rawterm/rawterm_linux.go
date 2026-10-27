// Package rawterm puts a terminal in raw mode, tells its size and decodes the
// keys a user types into it. It knows nothing of Baton.
package rawterm

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

func ioctl(f *os.File, request uintptr, arg unsafe.Pointer) error {
	conn, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var errno syscall.Errno
	if err := conn.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(arg))
	}); err != nil {
		return err
	}
	if errno != 0 {
		return errno
	}
	return nil
}

// Enable puts the terminal f in raw mode: no echo, no line editing, no signals
// from keys (Ctrl-C arrives as the byte 0x03), no translation of Enter, and no
// output processing, so a program that writes "\r\n" gets exactly that. The
// function it returns puts the terminal back as it was; it may be called more
// than once.
func Enable(f *os.File) (restore func() error, err error) {
	var saved syscall.Termios
	if err := ioctl(f, syscall.TCGETS, unsafe.Pointer(&saved)); err != nil {
		return nil, errors.New("not a terminal: " + err.Error())
	}
	raw := saved
	raw.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	raw.Oflag &^= syscall.OPOST
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB
	raw.Cflag |= syscall.CS8
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if err := ioctl(f, syscall.TCSETS, unsafe.Pointer(&raw)); err != nil {
		return nil, err
	}
	return func() error {
		return ioctl(f, syscall.TCSETS, unsafe.Pointer(&saved))
	}, nil
}

// Size is the number of columns and rows of the terminal f, or 0, 0 when it
// cannot tell (a pseudo-terminal nobody sized reports 0 as well).
func Size(f *os.File) (cols, rows int) {
	var ws struct{ Row, Col, X, Y uint16 }
	if err := ioctl(f, syscall.TIOCGWINSZ, unsafe.Pointer(&ws)); err != nil {
		return 0, 0
	}
	return int(ws.Col), int(ws.Row)
}
