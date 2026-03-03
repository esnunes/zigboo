// Command zigboo communicates with a Sonoff ZBDongle-E via EZSP/ASH.
//
// Usage:
//
//	zigboo --port /dev/ttyUSB0 reset     # ASH reset handshake
//	zigboo --port /dev/ttyUSB0 version   # EZSP version negotiation
//	zigboo --port /dev/ttyUSB0 info      # version + node-id + eui64
//	zigboo --port /dev/ttyUSB0 network  # network state and parameters
//	zigboo --port /dev/ttyUSB0 scan --type energy  # energy scan
//	zigboo --port /dev/ttyUSB0 scan --type active  # active scan
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
	"github.com/esnunes/zigboo/ezsp"
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
		fmt.Fprintf(os.Stderr, "  network   show network state and parameters\n")
		fmt.Fprintf(os.Stderr, "  scan      energy or active channel scan (--type energy|active)\n")
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
	case "network":
		return runNetwork(ctx, portPath)
	case "scan":
		return runScan(ctx, portPath, flag.Args()[1:])
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

// resetAndNegotiate opens a connection, performs ASH reset, and negotiates EZSP version.
// Returns the EZSP client ready for commands.
func resetAndNegotiate(ctx context.Context, portPath string) (*ezsp.Client, *ash.Conn, serial.Port, error) {
	conn, port, err := openConn(portPath)
	if err != nil {
		return nil, nil, nil, err
	}

	if _, _, err := conn.Reset(ctx); err != nil {
		conn.Close()
		port.Close()
		return nil, nil, nil, fmt.Errorf("reset: %w", err)
	}

	client := ezsp.New(conn)
	return client, conn, port, nil
}

func runVersion(ctx context.Context, portPath string) error {
	client, conn, port, err := resetAndNegotiate(ctx, portPath)
	if err != nil {
		return err
	}
	defer port.Close()
	defer conn.Close()
	_ = client

	info, err := client.NegotiateVersion(ctx)
	if err != nil {
		return fmt.Errorf("version: %w", err)
	}

	fmt.Printf("EZSP version negotiation successful\n")
	fmt.Printf("  Protocol version: %d\n", info.ProtocolVersion)
	fmt.Printf("  Stack type:       %d\n", info.StackType)
	fmt.Printf("  Stack version:    %s\n", info.StackVersionString())
	return nil
}

func runInfo(ctx context.Context, portPath string) error {
	client, conn, port, err := resetAndNegotiate(ctx, portPath)
	if err != nil {
		return err
	}
	defer port.Close()
	defer conn.Close()

	info, err := client.NegotiateVersion(ctx)
	if err != nil {
		return fmt.Errorf("info: %w", err)
	}

	nodeID, err := client.GetNodeID(ctx)
	if err != nil {
		return fmt.Errorf("info: %w", err)
	}

	eui64, err := client.GetEUI64(ctx)
	if err != nil {
		return fmt.Errorf("info: %w", err)
	}

	fmt.Printf("Dongle information\n")
	fmt.Printf("  EZSP version:  %d\n", info.ProtocolVersion)
	fmt.Printf("  Stack type:    %d\n", info.StackType)
	fmt.Printf("  Stack version: %s\n", info.StackVersionString())
	fmt.Printf("  Node ID:       0x%04X\n", nodeID)
	fmt.Printf("  EUI-64:        %s\n", ezsp.FormatEUI64(eui64))
	return nil
}

func runNetwork(ctx context.Context, portPath string) error {
	client, conn, port, err := resetAndNegotiate(ctx, portPath)
	if err != nil {
		return err
	}
	defer port.Close()
	defer conn.Close()

	if _, err := client.NegotiateVersion(ctx); err != nil {
		return fmt.Errorf("network: %w", err)
	}

	state, err := client.NetworkState(ctx)
	if err != nil {
		return fmt.Errorf("network: %w", err)
	}

	fmt.Printf("Network state: %s\n", state)

	if state == ezsp.NetworkStatusNoNetwork {
		return nil
	}

	nodeType, params, err := client.GetNetworkParameters(ctx)
	if err != nil {
		return fmt.Errorf("network: %w", err)
	}

	fmt.Printf("  PAN ID:        0x%04X\n", params.PanID)
	fmt.Printf("  Extended PAN:  %s\n", ezsp.FormatEUI64(params.ExtendedPanID))
	fmt.Printf("  Channel:       %d\n", params.RadioChannel)
	fmt.Printf("  TX power:      %d dBm\n", params.RadioTxPower)
	fmt.Printf("  Node type:     %s\n", nodeType)
	return nil
}

func runScan(ctx context.Context, portPath string, args []string) error {
	scanFlags := flag.NewFlagSet("scan", flag.ExitOnError)
	scanType := scanFlags.String("type", "energy", "scan type: energy or active")
	if err := scanFlags.Parse(args); err != nil {
		return err
	}

	client, conn, port, err := resetAndNegotiate(ctx, portPath)
	if err != nil {
		return err
	}
	defer port.Close()
	defer conn.Close()

	if _, err := client.NegotiateVersion(ctx); err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	switch *scanType {
	case "energy":
		results, errCh, err := client.StartEnergyScan(ctx, ezsp.DefaultChannelMask, 3)
		if err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		fmt.Printf("Channel  RSSI\n")
		for r := range results {
			fmt.Printf("  %5d  %d dBm\n", r.Channel, r.MaxRSSI)
		}
		if err := <-errCh; err != nil {
			return fmt.Errorf("scan: %w", err)
		}
	case "active":
		results, errCh, err := client.StartActiveScan(ctx, ezsp.DefaultChannelMask, 3)
		if err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		fmt.Printf("Channel  PAN ID  Extended PAN ID                 Join  Profile  LQI  RSSI\n")
		for r := range results {
			fmt.Printf("  %5d  0x%04X  %s  %5t  %7d  %3d  %d dBm\n",
				r.Channel, r.PanID, ezsp.FormatEUI64(r.ExtendedPanID),
				r.AllowingJoin, r.StackProfile, r.LQI, r.RSSI)
		}
		if err := <-errCh; err != nil {
			return fmt.Errorf("scan: %w", err)
		}
	default:
		return fmt.Errorf("scan: unknown type %q (use energy or active)", *scanType)
	}
	return nil
}
