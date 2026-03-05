package main

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/esnunes/zigboo/ezsp"
	"github.com/esnunes/zigboo/host"
	"github.com/esnunes/zigboo/zcl"
	"github.com/esnunes/zigboo/zdo"
)

// joinEvent represents a device join notification from the trust center.
type joinEvent struct {
	nodeID uint16
	eui64  [8]byte
	status byte
}

func runPair(ctx context.Context, portPath string, args []string) error {
	pairFlags := flag.NewFlagSet("pair", flag.ExitOnError)
	duration := pairFlags.Int("duration", 120, "permit-join duration in seconds (1-254, 255=indefinite)")
	if err := pairFlags.Parse(args); err != nil {
		return err
	}

	if *duration < 1 || *duration > 255 {
		return fmt.Errorf("pair: duration must be 1-255, got %d", *duration)
	}
	if *duration == 255 {
		fmt.Fprintf(os.Stderr, "warning: network will remain open indefinitely until explicitly closed\n")
	}

	// Phase 1: Setup connection and negotiate version.
	client, conn, port, err := resetAndNegotiate(ctx, portPath)
	if err != nil {
		return err
	}
	defer port.Close()
	defer conn.Close()

	versionInfo, err := client.NegotiateVersion(ctx)
	if err != nil {
		return fmt.Errorf("pair: %w", err)
	}

	// Phase 2: Register HA endpoint BEFORE NetworkInit.
	// The NCP requires endpoints to be registered while no network is active.
	if err := addEndpointRaw(ctx, client, 1, ezsp.ProfileIDHA, ezsp.DeviceIDConfigurationTool, nil, nil); err != nil {
		return fmt.Errorf("pair: register endpoint: %w", err)
	}

	// Restore network from NVM and verify it's active.
	if err := client.NetworkInit(ctx); err != nil {
		return fmt.Errorf("pair: no active network (run 'zigboo network init' first): %w", err)
	}

	state, err := client.NetworkState(ctx)
	if err != nil {
		return fmt.Errorf("pair: %w", err)
	}
	if state != ezsp.NetworkStatusJoinedNetwork {
		return fmt.Errorf("pair: no active network (state: %s). Run 'zigboo network init' first", state)
	}

	// Phase 3: Create host and start callback dispatch.
	// Use background context so the host stays alive for cleanup on Ctrl+C.
	extended := versionInfo.ProtocolVersion >= ezsp.ExtendedVersionThreshold
	h := host.New(conn, extended)

	// Log stack status changes (network up/down, permit-join open/closed).
	h.OnCallback(ezsp.FrameIDStackStatusHandler, func(params []byte) {
		if len(params) >= 1 {
			slog.Debug("pair: stackStatus", "status", fmt.Sprintf("0x%02X", params[0]))
		}
	})

	// Register trust center join handler → queue join events.
	joins := make(chan joinEvent, 16)
	h.OnCallback(ezsp.FrameIDTrustCenterJoinHandler, func(params []byte) {
		evt, err := decodeTrustCenterJoin(params)
		if err != nil {
			slog.Debug("pair: decode trustCenterJoin", "err", err)
			return
		}
		select {
		case joins <- evt:
		default:
			slog.Debug("pair: join queue full, dropping event")
		}
	})

	h.Start(context.Background())
	defer h.Close()

	// Phase 4: Open network for joining.
	resp, err := h.Command(ctx, ezsp.FrameIDPermitJoining, []byte{byte(*duration)})
	if err != nil {
		return fmt.Errorf("pair: permitJoining: %w", err)
	}
	if len(resp) < 1 || resp[0] != ezsp.EmberSuccess {
		return fmt.Errorf("pair: permitJoining: ember status 0x%02X", resp[0])
	}

	fmt.Printf("Network open for joining (%ds remaining)...\n\n", *duration)

	// Timer for permit-join expiry. Duration 255 means indefinite (Ctrl+C only).
	var deadline <-chan time.Time
	if *duration < 255 {
		deadline = time.After(time.Duration(*duration) * time.Second)
	}

	// Create ZDO and ZCL clients.
	zdoClient := zdo.New(h)
	zclClient := zcl.New(h)

	// Load device store.
	store := NewDeviceStore("devices.json")
	if err := store.Load(); err != nil {
		return fmt.Errorf("pair: %w", err)
	}

	// Phase 5: Process join events until deadline or Ctrl+C.
	paired := 0
	for {
		select {
		case evt := <-joins:
			if evt.status == ezsp.JoinStatusDeviceLeft {
				eui := ezsp.FormatEUI64(evt.eui64)
				fmt.Printf("[%s] Device left: 0x%04X (EUI64: %s)\n",
					time.Now().Format("15:04:05"), evt.nodeID, eui)
				continue
			}

			dev, err := interviewDevice(ctx, zdoClient, zclClient, evt)
			if err != nil {
				slog.Debug("pair: interview incomplete", "err", err)
			}

			if err := store.Save(dev); err != nil {
				fmt.Printf("  Error saving device: %v\n", err)
				continue
			}
			fmt.Printf("  Device saved.\n\n")
			paired++
			fmt.Printf("Waiting for more devices...\n")

		case <-deadline:
			fmt.Printf("\nPermit-join window closed.\n")
			fmt.Printf("%d device(s) paired.\n", paired)
			return nil

		case <-ctx.Done():
			// Ctrl+C: close permit-join early with a fresh context.
			fmt.Printf("\n")

			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
			resp, err := h.Command(cleanupCtx, ezsp.FrameIDPermitJoining, []byte{0})
			cleanupCancel()
			if err == nil && len(resp) > 0 && resp[0] == ezsp.EmberSuccess {
				fmt.Printf("Network closed for joining.\n")
			}

			fmt.Printf("%d device(s) paired.\n", paired)
			return nil
		}
	}
}

// decodeTrustCenterJoin decodes a trustCenterJoinHandler callback.
// Wire format: nodeId(2 LE) + eui64(8) + status(1) + policyDecision(1) + parentId(2) = 14 bytes.
func decodeTrustCenterJoin(params []byte) (joinEvent, error) {
	if len(params) < 14 {
		return joinEvent{}, fmt.Errorf("trustCenterJoin too short (%d bytes)", len(params))
	}
	var evt joinEvent
	evt.nodeID = binary.LittleEndian.Uint16(params[0:2])
	copy(evt.eui64[:], params[2:10])
	evt.status = params[10]
	return evt, nil
}

// interviewDevice runs the full ZDO + ZCL interview sequence for a newly joined device.
func interviewDevice(ctx context.Context, zdoClient *zdo.Client, zclClient *zcl.Client, evt joinEvent) (Device, error) {
	eui := ezsp.FormatEUI64(evt.eui64)
	fmt.Printf("[%s] Device joined: 0x%04X (EUI64: %s)\n",
		time.Now().Format("15:04:05"), evt.nodeID, eui)
	fmt.Printf("  Interviewing...\n")

	dev := Device{
		IEEE:     eui,
		NwkAddr:  fmt.Sprintf("0x%04X", evt.nodeID),
		LastSeen: time.Now(),
	}

	complete := true

	// Step 1: Node Descriptor → node type.
	nd, err := zdoClient.NodeDescriptor(ctx, evt.nodeID)
	if err != nil {
		slog.Debug("pair: nodeDescriptor failed", "err", err)
		fmt.Printf("  Node descriptor: failed (%v)\n", err)
		complete = false
	} else {
		switch nd.LogicalType {
		case zdo.LogicalTypeCoordinator:
			dev.NodeType = "coordinator"
		case zdo.LogicalTypeRouter:
			dev.NodeType = "router"
		case zdo.LogicalTypeEndDevice:
			dev.NodeType = "end-device"
		default:
			dev.NodeType = fmt.Sprintf("unknown(%d)", nd.LogicalType)
		}
		fmt.Printf("  Type: %s\n", nodeTypeDisplay(dev.NodeType))
	}

	// Step 2: Active Endpoints.
	eps, err := zdoClient.ActiveEndpoints(ctx, evt.nodeID)
	if err != nil {
		slog.Debug("pair: activeEndpoints failed", "err", err)
		fmt.Printf("  Active endpoints: failed (%v)\n", err)
		complete = false
	} else {
		fmt.Printf("  Endpoints: %d\n", len(eps))
	}

	// Step 3: Simple Descriptor per endpoint.
	var firstHAEndpoint uint8
	for _, ep := range eps {
		sd, err := zdoClient.SimpleDescriptor(ctx, evt.nodeID, ep)
		if err != nil {
			slog.Debug("pair: simpleDescriptor failed", "err", err, "endpoint", ep)
			fmt.Printf("    EP %d: failed (%v)\n", ep, err)
			complete = false
			continue
		}

		devEp := DeviceEndpoint{
			ID:        sd.Endpoint,
			ProfileID: fmt.Sprintf("0x%04X", sd.ProfileID),
			DeviceID:  fmt.Sprintf("0x%04X", sd.DeviceID),
		}
		for _, c := range sd.InputClusters {
			devEp.InClusters = append(devEp.InClusters, fmt.Sprintf("0x%04X", c))
		}
		for _, c := range sd.OutputClusters {
			devEp.OutClusters = append(devEp.OutClusters, fmt.Sprintf("0x%04X", c))
		}
		dev.Endpoints = append(dev.Endpoints, devEp)

		fmt.Printf("    EP %d: Profile %s, Device %s\n", ep, devEp.ProfileID, devEp.DeviceID)
		printClusters("In", devEp.InClusters)
		printClusters("Out", devEp.OutClusters)

		if sd.ProfileID == ezsp.ProfileIDHA && firstHAEndpoint == 0 {
			firstHAEndpoint = ep
		}
	}

	// Step 4: ZCL Basic cluster attributes (on first HA endpoint).
	if firstHAEndpoint > 0 {
		attrs, err := zclClient.ReadAttributes(ctx, evt.nodeID, 1, firstHAEndpoint,
			zcl.BasicClusterID, []uint16{zcl.AttrManufacturerName, zcl.AttrModelIdentifier, zcl.AttrSWBuildID})
		if err != nil {
			slog.Debug("pair: readAttributes failed", "err", err)
			fmt.Printf("  Basic attributes: failed (%v)\n", err)
			complete = false
		} else {
			if v, ok := attrs[zcl.AttrManufacturerName]; ok && v.Status == zcl.StatusSuccess {
				if s, ok := v.Value.(string); ok {
					dev.Manufacturer = s
					fmt.Printf("  Manufacturer: %s\n", s)
				}
			}
			if v, ok := attrs[zcl.AttrModelIdentifier]; ok && v.Status == zcl.StatusSuccess {
				if s, ok := v.Value.(string); ok {
					dev.Model = s
					fmt.Printf("  Model: %s\n", s)
				}
			}
			if v, ok := attrs[zcl.AttrSWBuildID]; ok && v.Status == zcl.StatusSuccess {
				if s, ok := v.Value.(string); ok {
					dev.Firmware = s
					fmt.Printf("  Firmware: %s\n", s)
				}
			}
		}
	}

	dev.InterviewComplete = complete

	if !complete {
		return dev, fmt.Errorf("interview incomplete for %s", eui)
	}
	return dev, nil
}

func printClusters(label string, clusters []string) {
	if len(clusters) == 0 {
		return
	}
	fmt.Printf("      %s: ", label)
	for i, c := range clusters {
		if i > 0 {
			fmt.Printf(", ")
		}
		fmt.Printf("%s", c)
	}
	fmt.Printf("\n")
}

func nodeTypeDisplay(t string) string {
	switch t {
	case "coordinator":
		return "Coordinator"
	case "router":
		return "Router"
	case "end-device":
		return "End Device"
	default:
		return t
	}
}

// addEndpointRaw registers a local endpoint on the NCP via the EZSP client.
// This must be called before NetworkInit because the NCP only accepts endpoint
// registration while no network is active.
func addEndpointRaw(ctx context.Context, client *ezsp.Client, endpoint uint8, profileID, deviceID uint16, inputClusters, outputClusters []uint16) error {
	params := make([]byte, 8+len(inputClusters)*2+len(outputClusters)*2)
	params[0] = endpoint
	binary.LittleEndian.PutUint16(params[1:3], profileID)
	binary.LittleEndian.PutUint16(params[3:5], deviceID)
	params[5] = 0x00 // appFlags: deviceVersion=0
	params[6] = byte(len(inputClusters))
	params[7] = byte(len(outputClusters))
	off := 8
	for _, c := range inputClusters {
		binary.LittleEndian.PutUint16(params[off:off+2], c)
		off += 2
	}
	for _, c := range outputClusters {
		binary.LittleEndian.PutUint16(params[off:off+2], c)
		off += 2
	}

	resp, err := client.Command(ctx, ezsp.FrameIDAddEndpoint, params)
	if err != nil {
		return fmt.Errorf("addEndpoint: %w", err)
	}
	if len(resp) < 1 || resp[0] != ezsp.EmberSuccess {
		return fmt.Errorf("addEndpoint: EZSP status 0x%02X", resp[0])
	}
	return nil
}
