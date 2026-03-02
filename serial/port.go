// Package serial provides access to serial communication ports.
//
// The Port interface abstracts platform-specific serial port implementations,
// enabling testing without hardware by substituting mock implementations.
package serial

import "io"

// Port provides access to a serial communication port.
//
// Implementations must be safe to close from a goroutine other than the one
// performing reads. The file descriptor is owned by the Port — callers must
// not close it directly.
type Port interface {
	io.ReadWriteCloser

	// Flush discards any unread input data buffered in the port.
	Flush() error
}

// Config holds serial port configuration.
type Config struct {
	// Path is the device path (e.g., "/dev/ttyUSB0" or "/dev/cu.usbserial-1420").
	Path string

	// BaudRate is the serial baud rate. Default: 115200.
	BaudRate int
}

// setDefaults fills in zero-valued fields with sensible defaults.
func (c *Config) setDefaults() {
	if c.BaudRate == 0 {
		c.BaudRate = 115200
	}
}
