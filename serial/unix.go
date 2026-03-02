//go:build unix

package serial

import (
	"fmt"
	"sync"

	"golang.org/x/sys/unix"
)

// unixPort implements Port using unix termios syscalls.
type unixPort struct {
	fd        int
	closeOnce sync.Once
}

// Open opens and configures a serial port.
//
// The returned Port owns the file descriptor. Callers must call Close
// when done. Close is safe to call from a goroutine other than the one
// performing reads.
func Open(cfg Config) (Port, error) {
	cfg.setDefaults()

	fd, err := unix.Open(cfg.Path, unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("serial: open %s: %w", cfg.Path, err)
	}

	if err := configure(fd, cfg.BaudRate); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("serial: configure %s: %w", cfg.Path, err)
	}

	return &unixPort{fd: fd}, nil
}

func (p *unixPort) Read(buf []byte) (int, error) {
	n, err := unix.Read(p.fd, buf)
	if err != nil {
		return 0, fmt.Errorf("serial: read: %w", err)
	}
	return n, nil
}

func (p *unixPort) Write(buf []byte) (int, error) {
	n, err := unix.Write(p.fd, buf)
	if err != nil {
		return 0, fmt.Errorf("serial: write: %w", err)
	}
	return n, nil
}

func (p *unixPort) Close() error {
	var closeErr error
	p.closeOnce.Do(func() {
		closeErr = unix.Close(p.fd)
	})
	if closeErr != nil {
		return fmt.Errorf("serial: close: %w", closeErr)
	}
	return nil
}

func (p *unixPort) Flush() error {
	if err := tcflush(p.fd); err != nil {
		return fmt.Errorf("serial: flush: %w", err)
	}
	return nil
}

// baudRateMap maps baud rate integers to termios constants.
var baudRateMap = map[int]uint64{
	9600:   unix.B9600,
	19200:  unix.B19200,
	38400:  unix.B38400,
	57600:  unix.B57600,
	115200: unix.B115200,
	230400: unix.B230400,
}

func configure(fd int, baudRate int) error {
	speed, ok := baudRateMap[baudRate]
	if !ok {
		return fmt.Errorf("unsupported baud rate: %d", baudRate)
	}

	termios, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		return fmt.Errorf("get termios: %w", err)
	}

	// cfmakeraw equivalent
	termios.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP |
		unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON | unix.IXOFF
	termios.Oflag &^= unix.OPOST
	termios.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	termios.Cflag &^= unix.CSIZE | unix.PARENB

	// 8N1
	termios.Cflag |= unix.CS8

	// No flow control (ASH handles flow control via byte stuffing)
	termios.Cflag &^= unix.CRTSCTS

	// CLOCAL: ignore modem control lines
	termios.Cflag |= unix.CLOCAL

	// HUPCL: clear (don't hang up on close)
	termios.Cflag &^= unix.HUPCL

	// CSTOPB: clear (1 stop bit)
	termios.Cflag &^= unix.CSTOPB

	// CREAD: enable receiver
	termios.Cflag |= unix.CREAD

	// VMIN=0, VTIME=1: Read returns after 100ms even if no bytes arrive.
	// This allows the reader goroutine to periodically check for context
	// cancellation and exit cleanly. With VMIN=1, Read blocks until at
	// least one byte arrives, causing hangs on shutdown when the dongle
	// is idle. See Go issue #10001 (Close vs Read race on fds).
	termios.Cc[unix.VMIN] = 0
	termios.Cc[unix.VTIME] = 1

	// Set baud rate
	termios.Ispeed = speed
	termios.Ospeed = speed

	if err := unix.IoctlSetTermios(fd, ioctlSetTermios, termios); err != nil {
		return fmt.Errorf("set termios: %w", err)
	}

	return nil
}
