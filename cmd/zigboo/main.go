// Command zigboo communicates with a Sonoff ZBDongle-E via EZSP/ASH.
//
// Usage:
//
//	zigboo --port /dev/ttyUSB0 reset     # ASH reset handshake
//	zigboo --port /dev/ttyUSB0 version   # EZSP version negotiation
//	zigboo --port /dev/ttyUSB0 info      # version + node-id + eui64
//	zigboo --port /dev/ttyUSB0 network state          # network state and parameters
//	zigboo --port /dev/ttyUSB0 network init            # form or resume network
//	zigboo --port /dev/ttyUSB0 network init --channel 15 --pan-id 0x1A2B
//	zigboo --port /dev/ttyUSB0 network permit-join --duration 60
//	zigboo --port /dev/ttyUSB0 scan --type energy  # energy scan
//	zigboo --port /dev/ttyUSB0 scan --type active  # active scan
//	zigboo --port /dev/ttyUSB0 endpoints # list registered endpoints
//	zigboo --port /dev/ttyUSB0 config list              # dump all config values
//	zigboo --port /dev/ttyUSB0 config get STACK_PROFILE  # read one config
//	zigboo --port /dev/ttyUSB0 config set MAX_HOPS 15    # write one config
//	zigboo -v --port /dev/ttyUSB0 info   # verbose mode with frame dumps
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"

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
		fmt.Fprintf(os.Stderr, "  network   network management (init|state|permit-join)\n")
		fmt.Fprintf(os.Stderr, "  scan      energy or active channel scan (--type energy|active)\n")
		fmt.Fprintf(os.Stderr, "  endpoints list registered endpoints and clusters\n")
		fmt.Fprintf(os.Stderr, "  config    get/set NCP configuration values (list|get|set)\n")
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
		return runNetwork(ctx, portPath, flag.Args()[1:])
	case "scan":
		return runScan(ctx, portPath, flag.Args()[1:])
	case "endpoints":
		return runEndpoints(ctx, portPath)
	case "config":
		return runConfig(ctx, portPath, flag.Args()[1:])
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

func runNetwork(ctx context.Context, portPath string, args []string) error {
	// Default to "state" when no subcommand is given (backward compat).
	sub := "state"
	if len(args) > 0 {
		sub = args[0]
	}

	switch sub {
	case "state":
		return runNetworkState(ctx, portPath)
	case "init":
		return runNetworkInit(ctx, portPath, args[1:])
	case "permit-join":
		return runNetworkPermitJoin(ctx, portPath, args[1:])
	default:
		return fmt.Errorf("network: unknown subcommand %q (use init, state, or permit-join)", sub)
	}
}

func runNetworkState(ctx context.Context, portPath string) error {
	client, conn, port, err := resetAndNegotiate(ctx, portPath)
	if err != nil {
		return err
	}
	defer port.Close()
	defer conn.Close()

	if _, err := client.NegotiateVersion(ctx); err != nil {
		return fmt.Errorf("network state: %w", err)
	}

	state, err := client.NetworkState(ctx)
	if err != nil {
		return fmt.Errorf("network state: %w", err)
	}

	fmt.Printf("Network state: %s\n", state)

	if state == ezsp.NetworkStatusNoNetwork {
		return nil
	}

	nodeType, params, err := client.GetNetworkParameters(ctx)
	if err != nil {
		return fmt.Errorf("network state: %w", err)
	}

	fmt.Printf("  PAN ID:        0x%04X\n", params.PanID)
	fmt.Printf("  Extended PAN:  %s\n", ezsp.FormatEUI64(params.ExtendedPanID))
	fmt.Printf("  Channel:       %d\n", params.RadioChannel)
	fmt.Printf("  TX power:      %d dBm\n", params.RadioTxPower)
	fmt.Printf("  Node type:     %s\n", nodeType)
	return nil
}

func runNetworkInit(ctx context.Context, portPath string, args []string) error {
	initFlags := flag.NewFlagSet("network init", flag.ExitOnError)
	channel := initFlags.Int("channel", 11, "Zigbee channel (11-26)")
	panID := initFlags.Int("pan-id", 0xFFFF, "PAN ID (0xFFFF for auto-select)")
	txPower := initFlags.Int("tx-power", 8, "TX power in dBm")
	if err := initFlags.Parse(args); err != nil {
		return err
	}

	client, conn, port, err := resetAndNegotiate(ctx, portPath)
	if err != nil {
		return err
	}
	defer port.Close()
	defer conn.Close()

	if _, err := client.NegotiateVersion(ctx); err != nil {
		return fmt.Errorf("network init: %w", err)
	}

	// Guard: if already joined, show state and exit without modifying security.
	state, err := client.NetworkState(ctx)
	if err != nil {
		return fmt.Errorf("network init: %w", err)
	}
	if state == ezsp.NetworkStatusJoinedNetwork {
		fmt.Printf("Network already active\n")
		return printNetworkDetails(ctx, client)
	}

	// Set initial security state with HA defaults.
	var networkKey [16]byte
	if _, err := rand.Read(networkKey[:]); err != nil {
		return fmt.Errorf("network init: generate network key: %w", err)
	}

	secState := ezsp.EmberInitialSecurityState{
		Bitmask: ezsp.EmberHavePreconfiguredKey | ezsp.EmberHaveNetworkKey |
			ezsp.EmberTrustCenterGlobalLinkKey | ezsp.EmberHaveTrustCenterEUI64,
		PreconfiguredKey: ezsp.ZigbeeHALinkKey,
		NetworkKey:       networkKey,
	}
	if err := client.SetInitialSecurityState(ctx, secState); err != nil {
		return fmt.Errorf("network init: %w", err)
	}

	// Try to resume a stored network.
	resumed := true
	if err := client.NetworkInit(ctx); err != nil {
		// No stored network — form a new one.
		resumed = false
		np := ezsp.NetworkParameters{
			PanID:        uint16(*panID),
			RadioTxPower: int8(*txPower),
			RadioChannel: uint8(*channel),
		}
		if err := client.FormNetwork(ctx, np); err != nil {
			return fmt.Errorf("network init: %w", err)
		}
	}

	if resumed {
		fmt.Printf("Network resumed\n")
	} else {
		fmt.Printf("Network formed\n")
	}
	return printNetworkDetails(ctx, client)
}

func printNetworkDetails(ctx context.Context, client *ezsp.Client) error {
	state, err := client.NetworkState(ctx)
	if err != nil {
		return fmt.Errorf("network: %w", err)
	}
	fmt.Printf("  State:         %s\n", state)

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

func runNetworkPermitJoin(ctx context.Context, portPath string, args []string) error {
	pjFlags := flag.NewFlagSet("network permit-join", flag.ExitOnError)
	duration := pjFlags.Int("duration", 60, "join window duration in seconds (0=close, 255=indefinite)")
	if err := pjFlags.Parse(args); err != nil {
		return err
	}

	if *duration < 0 || *duration > 255 {
		return fmt.Errorf("network permit-join: duration must be 0-255, got %d", *duration)
	}

	client, conn, port, err := resetAndNegotiate(ctx, portPath)
	if err != nil {
		return err
	}
	defer port.Close()
	defer conn.Close()

	if _, err := client.NegotiateVersion(ctx); err != nil {
		return fmt.Errorf("network permit-join: %w", err)
	}

	if err := client.PermitJoining(ctx, uint8(*duration)); err != nil {
		return fmt.Errorf("network permit-join: %w", err)
	}

	if *duration == 0 {
		fmt.Printf("Permit joining: closed\n")
	} else {
		fmt.Printf("Permit joining: open for %d seconds\n", *duration)
	}
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

func runEndpoints(ctx context.Context, portPath string) error {
	client, conn, port, err := resetAndNegotiate(ctx, portPath)
	if err != nil {
		return err
	}
	defer port.Close()
	defer conn.Close()

	if _, err := client.NegotiateVersion(ctx); err != nil {
		return fmt.Errorf("endpoints: %w", err)
	}

	count, err := client.GetEndpointCount(ctx)
	if err != nil {
		return fmt.Errorf("endpoints: %w\n  (your NCP firmware may not support the endpoint query commands)", err)
	}

	fmt.Printf("Endpoints: %d\n", count)
	if count == 0 {
		return nil
	}

	for i := uint8(0); i < count; i++ {
		ep, err := client.GetEndpoint(ctx, i)
		if err != nil {
			fmt.Printf("\n  error reading endpoint at index %d: %v\n", i, err)
			continue
		}

		desc, err := client.GetEndpointDescription(ctx, ep)
		if err != nil {
			fmt.Printf("\nEndpoint %d\n", ep)
			fmt.Printf("  error reading description: %v\n", err)
			continue
		}

		fmt.Printf("\nEndpoint %d\n", ep)
		fmt.Printf("  Profile:    0x%04X\n", desc.ProfileID)
		fmt.Printf("  Device ID:  0x%04X\n", desc.DeviceID)
		fmt.Printf("  Version:    %d\n", desc.DeviceVersion)

		if desc.InputClusterCount > 0 {
			fmt.Printf("  In clusters: ")
			for j := uint8(0); j < desc.InputClusterCount; j++ {
				cluster, err := client.GetEndpointCluster(ctx, ep, 0, j)
				if err != nil {
					fmt.Printf(" error: %v", err)
					break
				}
				if j > 0 {
					fmt.Printf(", ")
				}
				fmt.Printf("0x%04X", cluster)
			}
			fmt.Printf("\n")
		} else {
			fmt.Printf("  In clusters:  (none)\n")
		}

		if desc.OutputClusterCount > 0 {
			fmt.Printf("  Out clusters: ")
			for j := uint8(0); j < desc.OutputClusterCount; j++ {
				cluster, err := client.GetEndpointCluster(ctx, ep, 1, j)
				if err != nil {
					fmt.Printf(" error: %v", err)
					break
				}
				if j > 0 {
					fmt.Printf(", ")
				}
				fmt.Printf("0x%04X", cluster)
			}
			fmt.Printf("\n")
		} else {
			fmt.Printf("  Out clusters: (none)\n")
		}
	}
	return nil
}

func runConfig(ctx context.Context, portPath string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("config: subcommand required (list, get, set)")
	}
	sub := args[0]

	client, conn, port, err := resetAndNegotiate(ctx, portPath)
	if err != nil {
		return err
	}
	defer port.Close()
	defer conn.Close()

	if _, err := client.NegotiateVersion(ctx); err != nil {
		return fmt.Errorf("config: %w", err)
	}

	switch sub {
	case "list":
		for _, id := range ezsp.AllConfigIDs {
			val, err := client.GetConfigurationValue(ctx, id)
			if err != nil {
				fmt.Printf("%-42s (0x%02X): unsupported\n", id, uint8(id))
				continue
			}
			fmt.Printf("%-42s (0x%02X): %d\n", id, uint8(id), val)
		}
		return nil

	case "get":
		if len(args) < 2 {
			return fmt.Errorf("config get: name or hex ID required")
		}
		id, err := ezsp.ParseConfigID(args[1])
		if err != nil {
			return fmt.Errorf("config get: %w", err)
		}
		val, err := client.GetConfigurationValue(ctx, id)
		if err != nil {
			return fmt.Errorf("config get: %w", err)
		}
		fmt.Printf("%s (0x%02X): %d\n", id, uint8(id), val)
		return nil

	case "set":
		if len(args) < 3 {
			return fmt.Errorf("config set: name/hex ID and value required")
		}
		id, err := ezsp.ParseConfigID(args[1])
		if err != nil {
			return fmt.Errorf("config set: %w", err)
		}
		v, err := strconv.ParseUint(args[2], 0, 16)
		if err != nil {
			return fmt.Errorf("config set: invalid value %q: %w", args[2], err)
		}
		if err := client.SetConfigurationValue(ctx, id, uint16(v)); err != nil {
			return fmt.Errorf("config set: %w", err)
		}
		fmt.Printf("%s (0x%02X): set to %d\n", id, uint8(id), v)
		return nil

	default:
		return fmt.Errorf("config: unknown subcommand %q (use list, get, or set)", sub)
	}
}
