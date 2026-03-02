//go:build darwin

package serial

import "golang.org/x/sys/unix"

const (
	ioctlGetTermios = unix.TIOCGETA
	ioctlSetTermios = unix.TIOCSETA
)

func tcflush(fd int) error {
	// TIOCFLUSH on macOS/BSD expects a pointer to an int (the queue selector).
	// IoctlSetPointerInt passes &value, unlike IoctlSetInt which passes
	// the value directly (causing EFAULT).
	return unix.IoctlSetPointerInt(fd, unix.TIOCFLUSH, unix.TCIFLUSH)
}
