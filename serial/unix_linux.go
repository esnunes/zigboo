//go:build linux

package serial

import "golang.org/x/sys/unix"

const (
	ioctlGetTermios = unix.TCGETS
	ioctlSetTermios = unix.TCSETS
)

func tcflush(fd int) error {
	// TCFLSH on Linux takes the queue selector as a plain int value.
	return unix.IoctlSetInt(fd, unix.TCFLSH, unix.TCIFLUSH)
}
