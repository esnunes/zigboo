//go:build linux

package serial

import "golang.org/x/sys/unix"

const (
	ioctlGetTermios = unix.TCGETS
	ioctlSetTermios = unix.TCSETS
)

func tcflush(fd int) error {
	_, err := unix.IoctlGetInt(fd, unix.TCFLSH)
	return err
}
