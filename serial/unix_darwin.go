//go:build darwin

package serial

import "golang.org/x/sys/unix"

const (
	ioctlGetTermios = unix.TIOCGETA
	ioctlSetTermios = unix.TIOCSETA
)

func tcflush(fd int) error {
	return unix.IoctlSetInt(fd, unix.TIOCFLUSH, unix.TCIFLUSH)
}
