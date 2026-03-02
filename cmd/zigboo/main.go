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
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
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

func runReset(_ context.Context, _ string) error {
	return fmt.Errorf("not implemented")
}

func runVersion(_ context.Context, _ string) error {
	return fmt.Errorf("not implemented")
}

func runInfo(_ context.Context, _ string) error {
	return fmt.Errorf("not implemented")
}
