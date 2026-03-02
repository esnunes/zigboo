// Command zigboo communicates with a Sonoff ZBDongle-E via EZSP/ASH.
//
// Usage:
//
//	zigboo --port /dev/ttyUSB0 reset     # ASH reset handshake
//	zigboo --port /dev/ttyUSB0 version   # EZSP version negotiation
//	zigboo --port /dev/ttyUSB0 info      # version + node-id + eui64
//	zigboo -v --port /dev/ttyUSB0 info   # verbose mode with frame dumps
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"

	"github.com/esnunes/zigboo/ash"
	"github.com/esnunes/zigboo/serial"
)

func main() {
	var (
		portPath = flag.String("port", "", "serial port path (e.g., /dev/ttyUSB0)")
		verbose  = flag.Bool("v", false, "enable verbose debug logging")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: zigboo [flags] <command>\n\n")
		fmt.Fprintf(os.Stderr, "Commands:\n")
		fmt.Fprintf(os.Stderr, "  reset     perform ASH reset handshake\n")
		fmt.Fprintf(os.Stderr, "  version   negotiate EZSP protocol version\n")
		fmt.Fprintf(os.Stderr, "  info      print version, node ID, and EUI-64\n")
		fmt.Fprintf(os.Stderr, "\nFlags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	cmd := flag.Arg(0)
	if cmd == "" {
		flag.Usage()
		os.Exit(1)
	}

	if *portPath == "" {
		fmt.Fprintf(os.Stderr, "error: --port is required\n")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := run(ctx, *portPath, cmd); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, portPath string, cmd string) error {
	switch cmd {
	case "reset":
		return runReset(ctx, portPath)
	case "version":
		return runVersion(ctx, portPath)
	case "info":
		return runInfo(ctx, portPath)
	default:
		return fmt.Errorf("unknown command: %s", cmd)
	}
}

// openConn opens a serial port and creates an ASH connection.
// The caller must close both the connection and the port.
func openConn(portPath string) (*ash.Conn, serial.Port, error) {
	port, err := serial.Open(serial.Config{Path: portPath})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, fmt.Errorf("device not found: %s — check the path and ensure the dongle is plugged in", portPath)
		}
		if errors.Is(err, os.ErrPermission) {
			return nil, nil, fmt.Errorf("permission denied: %s — add your user to the 'dialout' group (Linux) or check System Settings > Privacy (macOS)", portPath)
		}
		return nil, nil, err
	}
	conn := ash.New(port)
	return conn, port, nil
}

func runReset(ctx context.Context, portPath string) error {
	conn, port, err := openConn(portPath)
	if err != nil {
		return err
	}
	defer port.Close()
	defer conn.Close()

	version, resetCode, err := conn.Reset(ctx)
	if err != nil {
		return fmt.Errorf("reset: %w", err)
	}

	fmt.Printf("ASH reset successful\n")
	fmt.Printf("  Protocol version: %d\n", version)
	fmt.Printf("  Reset code:       %d\n", resetCode)
	return nil
}

func runVersion(_ context.Context, _ string) error {
	return fmt.Errorf("not implemented")
}

func runInfo(_ context.Context, _ string) error {
	return fmt.Errorf("not implemented")
}
