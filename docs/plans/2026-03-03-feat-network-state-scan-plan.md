---
title: "feat: Add networkState, getNetworkParameters, and startScan"
type: feat
date: 2026-03-03
brainstorm: docs/brainstorms/2026-03-03-network-state-scan-brainstorm.md
---

# feat: Add networkState, getNetworkParameters, and startScan

## Overview

Add three EZSP commands that provide visibility into the NCP's network state and RF environment. Two are simple request/response commands (`networkState`, `getNetworkParameters`), one is the first async command requiring callback processing (`startScan`). This also introduces `ash.Conn.Recv()` for receiving unsolicited DATA frames and adds `network` and `scan` CLI subcommands.

## Problem Statement

After Milestone 1 (ASH/EZSP foundation), the coordinator can identify itself (`info`) but has no visibility into the network. Before forming or joining a network, we need to:
1. Query whether the NCP has an existing network
2. Read network parameters (PAN ID, channel, etc.)
3. Scan the RF environment to find the quietest channel or discover existing networks

## Proposed Solution

Follow existing EZSP command patterns for the simple commands. For `startScan`, add minimal async infrastructure:
- New `ash.Conn.Recv()` method for receiving unsolicited frames
- Channel-based streaming API at the EZSP layer
- Scan exclusivity via a state flag (no concurrent commands during scan)

## Technical Approach

### Architecture

No new packages. Changes are contained within `ash/`, `ezsp/`, and `cmd/zigboo/`.

```
cmd/zigboo/main.go   +runNetwork(), +runScan()
ezsp/ezsp.go         +NetworkState(), +GetNetworkParameters(), +StartEnergyScan(), +StartActiveScan()
ezsp/commands.go     +frame ID constants for new commands and callbacks
ezsp/types.go        +type definitions (enums, structs, results)  [NEW FILE]
ash/ash.go           +Recv()
```

### Key Design Decisions (from SpecFlow analysis)

**Frame ownership during scan (Gap 1):** Add a `scanning bool` field and a `mu sync.Mutex` to `ezsp.Client`. `Command()` returns `ErrScanInProgress` when `scanning` is true. `StartEnergyScan`/`StartActiveScan` set it before spawning the goroutine; the goroutine clears it on exit. This prevents frame channel races without a full demux layer.

**Recv() does NOT advance frmNum (Gap 7):** `frmNum` is the host's outgoing sequence number. `Recv()` only receives unsolicited NCP frames, so only `ackNum` (acknowledging the NCP's frame) is advanced. `frmNum` is untouched.

**Recv() ignores standalone ACK/NAK (Gap 6):** During a scan, the host is not sending DATA frames, so standalone ACK/NAK frames are meaningless. `Recv()` logs and skips them, continuing to wait for the next DATA frame.

**Recv() uses context for timeout (Gap 5, Q9):** No internal timer. The caller controls timeouts via `ctx`. For scans, the CLI can set a generous deadline or rely on Ctrl+C.

**Error channel buffer = 1 (Gap 18, Q3):** Prevents goroutine leak if consumer doesn't read the error channel. Result channel buffer = 16 (number of Zigbee channels).

**Scan exclusivity (Gap 1):** Simplest approach. A full demux layer is Milestone 3 scope.

### Implementation Phases

#### Phase 1: Type Definitions

Create `ezsp/types.go` with all types and constants needed by the new commands.

**Constants and enums:**

```go
// ezsp/types.go

// EmberNetworkStatus represents the NCP's current network state.
type EmberNetworkStatus byte

const (
    NetworkStatusNoNetwork      EmberNetworkStatus = 0x00
    NetworkStatusJoiningNetwork EmberNetworkStatus = 0x01
    NetworkStatusJoinedNetwork  EmberNetworkStatus = 0x02
    NetworkStatusJoinedNoParent EmberNetworkStatus = 0x03
    NetworkStatusLeavingNetwork EmberNetworkStatus = 0x04
)

// String returns a human-readable name for the network status.
func (s EmberNetworkStatus) String() string

// EmberNodeType represents the type of a node in the network.
type EmberNodeType byte

const (
    NodeTypeUnknown        EmberNodeType = 0x00
    NodeTypeCoordinator    EmberNodeType = 0x01
    NodeTypeRouter         EmberNodeType = 0x02
    NodeTypeEndDevice      EmberNodeType = 0x03
    NodeTypeSleepyEndDevice EmberNodeType = 0x04
)

// String returns a human-readable name for the node type.
func (t EmberNodeType) String() string

// EzspNetworkScanType identifies the type of network scan.
type EzspNetworkScanType byte

const (
    ScanTypeEnergy EzspNetworkScanType = 0x00
    ScanTypeActive EzspNetworkScanType = 0x01
)

// NetworkParameters holds the network configuration from getNetworkParameters.
type NetworkParameters struct {
    ExtendedPanID [8]byte
    PanID         uint16
    RadioTxPower  int8
    RadioChannel  uint8
}

// EnergyScanResult holds the result of an energy scan for a single channel.
type EnergyScanResult struct {
    Channel  uint8
    MaxRSSI  int8
}

// NetworkScanResult holds the result of an active scan for a discovered network.
type NetworkScanResult struct {
    Channel       uint8
    PanID         uint16
    ExtendedPanID [8]byte
    AllowingJoin  bool
    StackProfile  uint8
    NwkUpdateID   uint8
    LQI           uint8
    RSSI          int8
}
```

**Frame ID constants to add to `ezsp/commands.go`:**

```go
// frameIDNetworkState queries the current network state (0x0018).
frameIDNetworkState = 0x0018

// frameIDStartScan initiates an energy or active scan (0x001A).
frameIDStartScan = 0x001A

// frameIDNetworkFoundHandler is the callback for active scan results (0x001B).
frameIDNetworkFoundHandler = 0x001B

// frameIDScanCompleteHandler is the callback when a scan finishes (0x001C).
frameIDScanCompleteHandler = 0x001C

// frameIDGetNetworkParameters reads current network parameters (0x0028).
frameIDGetNetworkParameters = 0x0028

// frameIDEnergyScanResultHandler is the callback for energy scan results (0x0048).
frameIDEnergyScanResultHandler = 0x0048
```

- [x] Create `ezsp/types.go` with all type definitions, constants, and `String()` methods
- [x] Add frame ID constants to `ezsp/commands.go`
- [x] Add tests for `String()` methods in `ezsp/types_test.go`

#### Phase 2: ash.Conn.Recv()

Add a method to receive unsolicited DATA frames from the NCP.

```go
// ash/ash.go

// Recv receives the next unsolicited DATA frame from the NCP.
//
// It reads frames from the internal channel, sends an ACK for each received
// DATA frame, and returns the payload. Standalone ACK and NAK frames are
// silently skipped (they have no meaning when the host is not sending).
// Returns ErrConnectionReset if a RSTACK frame is received.
//
// Unlike Send/waitForResponse, Recv does NOT advance frmNum because the host
// is not sending an outgoing DATA frame. Only ackNum is advanced to acknowledge
// the NCP's frame.
//
// The caller controls timeouts via ctx. There is no internal timer.
func (c *Conn) Recv(ctx context.Context) ([]byte, error) {
    for {
        select {
        case <-ctx.Done():
            return nil, ctx.Err()
        case raw, ok := <-c.frames:
            if !ok {
                return nil, fmt.Errorf("ash: recv: reader closed")
            }

            control, payload, err := decodeFrame(raw)
            if err != nil {
                slog.Debug("ash: recv: decode error, ignoring", "err", err)
                continue
            }

            switch frameType(control) {
            case frameTypeACK, frameTypeNAK:
                // No outgoing frame to ACK/NAK — skip.
                slog.Debug("ash: recv: ignoring standalone ACK/NAK", "control", fmt.Sprintf("0x%02X", control))
                continue

            case frameTypeDATA:
                frmNum, _, _ := parseDataControl(control)

                // Send ACK for this frame.
                nextAck := (frmNum + 1) & 0x07
                c.ackNum = nextAck
                ackFrame := encodeACK(nextAck)
                if _, err := c.port.Write(ackFrame); err != nil {
                    slog.Warn("ash: recv: send ACK failed", "err", err)
                }
                // NOTE: frmNum is NOT advanced — no outgoing frame was sent.

                return payload, nil

            case frameTypeRSTACK:
                return nil, ErrConnectionReset

            default:
                slog.Debug("ash: recv: unexpected frame", "control", fmt.Sprintf("0x%02X", control))
            }
        }
    }
}
```

- [x] Implement `Recv()` in `ash/ash.go`
- [x] Add tests for `Recv()` in `ash/ash_test.go`:
  - Receives DATA frame, sends ACK, returns payload
  - Skips standalone ACK frames
  - Skips standalone NAK frames
  - Returns `ErrConnectionReset` on RSTACK
  - Returns `ctx.Err()` on context cancellation
  - Returns error when frames channel is closed
  - Does NOT advance `frmNum` (verify via subsequent Send)
  - Handles multiple consecutive DATA frames (scan simulation)

#### Phase 3: Simple EZSP Commands

Add `NetworkState()` and `GetNetworkParameters()` following the existing `GetNodeID`/`GetEUI64` pattern.

```go
// ezsp/ezsp.go

// NetworkState returns the current network state of the NCP.
func (c *Client) NetworkState(ctx context.Context) (EmberNetworkStatus, error) {
    resp, err := c.Command(ctx, frameIDNetworkState, nil)
    if err != nil {
        return 0, fmt.Errorf("ezsp: networkState: %w", err)
    }
    if len(resp) < 1 {
        return 0, fmt.Errorf("ezsp: networkState: response too short (%d bytes)", len(resp))
    }
    return EmberNetworkStatus(resp[0]), nil
}

// GetNetworkParameters returns the current network parameters and node type.
// Returns an error if the NCP is not joined to a network (EmberStatus != success).
func (c *Client) GetNetworkParameters(ctx context.Context) (EmberNodeType, NetworkParameters, error) {
    resp, err := c.Command(ctx, frameIDGetNetworkParameters, nil)
    if err != nil {
        return 0, NetworkParameters{}, fmt.Errorf("ezsp: getNetworkParameters: %w", err)
    }
    // Response: EmberStatus(1) + EmberNodeType(1) + ExtendedPanID(8) + PanID(2) + TxPower(1) + Channel(1) = 14
    if len(resp) < 14 {
        return 0, NetworkParameters{}, fmt.Errorf("ezsp: getNetworkParameters: response too short (%d bytes)", len(resp))
    }
    if resp[0] != 0x00 { // EmberStatus success
        return 0, NetworkParameters{}, fmt.Errorf("ezsp: getNetworkParameters: ember status 0x%02X", resp[0])
    }
    nodeType := EmberNodeType(resp[1])
    var params NetworkParameters
    copy(params.ExtendedPanID[:], resp[2:10])
    params.PanID = binary.LittleEndian.Uint16(resp[10:12])
    params.RadioTxPower = int8(resp[12])
    params.RadioChannel = resp[13]
    return nodeType, params, nil
}
```

- [x] Add `NetworkState()` to `ezsp/ezsp.go`
- [x] Add `GetNetworkParameters()` to `ezsp/ezsp.go`
- [x] Add tests in `ezsp/ezsp_test.go`:
  - `TestNetworkState`: table-driven with all 5 status values
  - `TestNetworkState_ResponseTooShort`: error on empty response
  - `TestGetNetworkParameters`: happy path with known byte sequence
  - `TestGetNetworkParameters_EmberStatusError`: non-zero status returns error
  - `TestGetNetworkParameters_ResponseTooShort`: error on truncated response

#### Phase 4: Scan Commands (Async)

This is the most complex phase. Add scan exclusivity, the scan goroutine, and callback frame decoding.

**Step 4a: Add scan exclusivity to Client**

```go
// ezsp/ezsp.go

// Add to Client struct:
type Client struct {
    conn     *ash.Conn
    seq      byte
    extended bool
    version  byte
    mu       sync.Mutex
    scanning bool
}

// ErrScanInProgress is returned when a command is issued while a scan is running.
var ErrScanInProgress = errors.New("ezsp: scan in progress")

// Update Command() to check scanning state:
func (c *Client) Command(ctx context.Context, frameID uint16, params []byte) ([]byte, error) {
    c.mu.Lock()
    if c.scanning {
        c.mu.Unlock()
        return nil, ErrScanInProgress
    }
    c.mu.Unlock()
    // ... existing implementation
}
```

**Step 4b: Implement StartEnergyScan and StartActiveScan**

```go
// ezsp/ezsp.go

// DefaultChannelMask covers Zigbee 2.4 GHz channels 11-26.
const DefaultChannelMask = 0x07FFF800

// StartEnergyScan initiates an energy scan and returns a channel of results.
//
// It blocks until the NCP confirms the scan has started. On success, it spawns
// a goroutine that reads energy scan results and sends them on the returned
// channel. The result channel is closed when scanCompleteHandler arrives.
// The error channel (buffered, cap 1) receives the scan completion status
// (nil on success) and is then closed.
//
// No other EZSP commands may be issued while a scan is in progress.
// Command() will return ErrScanInProgress.
func (c *Client) StartEnergyScan(ctx context.Context, channelMask uint32, duration uint8) (<-chan EnergyScanResult, <-chan error, error)

// StartActiveScan initiates an active scan and returns a channel of results.
// Same contract as StartEnergyScan but returns NetworkScanResult.
func (c *Client) StartActiveScan(ctx context.Context, channelMask uint32, duration uint8) (<-chan NetworkScanResult, <-chan error, error)
```

Internal implementation pattern (shared between both):
1. Lock `c.mu`, set `c.scanning = true`, unlock
2. Encode params: `[scanType(1), channelMask(4 LE), duration(1)]`
3. Call `c.Command()` — wait, this won't work because `Command()` now checks `c.scanning`. Need to use `c.sendCommand()` (a non-checking variant) or set `scanning` after the initial command returns.

Correction: Set `scanning = true` AFTER the initial command response confirms success. The flow is:
1. Send startScan command via `c.Command()` (scanning is still false)
2. Check EmberStatus in response
3. If success: lock mu, set scanning=true, unlock, spawn goroutine
4. Goroutine: loop `Recv()`, decode callbacks, send on channels
5. On scanComplete or error: lock mu, set scanning=false, unlock, close channels

```go
func (c *Client) startScan(ctx context.Context, scanType EzspNetworkScanType, channelMask uint32, duration uint8) error {
    params := make([]byte, 6)
    params[0] = byte(scanType)
    binary.LittleEndian.PutUint32(params[1:5], channelMask)
    params[5] = duration

    resp, err := c.Command(ctx, frameIDStartScan, params)
    if err != nil {
        return fmt.Errorf("ezsp: startScan: %w", err)
    }
    if len(resp) < 1 {
        return fmt.Errorf("ezsp: startScan: response too short (%d bytes)", len(resp))
    }
    if resp[0] != 0x00 {
        return fmt.Errorf("ezsp: startScan: ember status 0x%02X", resp[0])
    }
    return nil
}
```

**Step 4c: Callback decoding helper**

```go
// ezsp/ezsp.go

// decodeCallback decodes an EZSP callback frame and returns the frame ID and parameters.
func (c *Client) decodeCallback(raw []byte) (frameID uint16, params []byte, err error) {
    if c.extended {
        _, frameID, params, err = decodeExtended(raw)
    } else {
        _, frameID, params, err = decodeLegacy(raw)
    }
    return
}
```

- [x] Add `sync.Mutex`, `scanning bool`, `ErrScanInProgress` to `ezsp/ezsp.go`
- [x] Add scanning check to `Command()`
- [x] Implement `startScan()` internal helper
- [x] Implement `decodeCallback()` helper
- [x] Implement `StartEnergyScan()` with goroutine:
  - Calls `startScan()`, on success sets `scanning=true`
  - Creates result channel (cap 16) and error channel (cap 1)
  - Spawns goroutine: loop `c.conn.Recv(ctx)`, decode callback, dispatch by frame ID
  - `energyScanResultHandler` (0x0048): parse channel(1) + maxRssi(1), send on result channel
  - `scanCompleteHandler` (0x001C): parse channel(1) + status(1), close result channel, send error if status != 0, close error channel, set `scanning=false`
  - Unexpected frame IDs: log debug, skip
  - `Recv` error: close result channel, send error on error channel, close error channel, set `scanning=false`
- [x] Implement `StartActiveScan()` with same pattern:
  - `networkFoundHandler` (0x001B): parse channel(1) + panId(2 LE) + extPanId(8) + allowingJoin(1) + stackProfile(1) + nwkUpdateId(1) + lqi(1) + rssi(1), send on result channel
- [x] Add tests in `ezsp/ezsp_test.go`:
  - `TestStartEnergyScan`: mock port feeds startScan response + 3 energy results + scanComplete
  - `TestStartEnergyScan_ScanFailed`: startScan response with non-zero status
  - `TestStartEnergyScan_ContextCancelled`: cancel ctx mid-scan, verify goroutine exits
  - `TestStartActiveScan`: mock port feeds startScan response + 2 network results + scanComplete
  - `TestStartActiveScan_ZeroResults`: scanComplete with no prior results
  - `TestScanExclusivity`: start scan, verify Command() returns ErrScanInProgress, verify cleared after scan completes
  - `TestStartEnergyScan_UnexpectedCallback`: verify unexpected frame IDs are skipped
  - `TestStartEnergyScan_ScanCompleteWithError`: scanComplete with non-zero status

#### Phase 5: CLI Commands

**`zigboo network` command:**

```go
// cmd/zigboo/main.go

func runNetwork(ctx context.Context, portPath string) error {
    client, conn, port, err := resetAndNegotiate(ctx, portPath)
    if err != nil {
        return err
    }
    defer port.Close()
    defer conn.Close()

    info, err := client.NegotiateVersion(ctx)
    if err != nil {
        return fmt.Errorf("network: %w", err)
    }
    _ = info

    state, err := client.NetworkState(ctx)
    if err != nil {
        return fmt.Errorf("network: %w", err)
    }

    fmt.Printf("Network state: %s\n", state)

    if state == ezsp.NetworkStatusNoNetwork {
        return nil // No network — skip parameters.
    }

    nodeType, params, err := client.GetNetworkParameters(ctx)
    if err != nil {
        return fmt.Errorf("network: %w", err)
    }

    fmt.Printf("PAN ID:        0x%04X\n", params.PanID)
    fmt.Printf("Extended PAN:  %s\n", ezsp.FormatEUI64(params.ExtendedPanID))
    fmt.Printf("Channel:       %d\n", params.RadioChannel)
    fmt.Printf("TX power:      %d dBm\n", params.RadioTxPower)
    fmt.Printf("Node type:     %s\n", nodeType)
    return nil
}
```

**`zigboo scan --type energy|active` command:**

The `scan` command needs its own FlagSet for the `--type` flag since the top-level `flag.Parse()` has already consumed global flags.

```go
func runScan(ctx context.Context, portPath string, args []string) error {
    scanFlags := flag.NewFlagSet("scan", flag.ExitOnError)
    scanType := scanFlags.String("type", "energy", "scan type: energy or active")
    scanFlags.Parse(args)

    client, conn, port, err := resetAndNegotiate(ctx, portPath)
    // ...negotiate version...

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
        // ... similar pattern, different output format ...
    default:
        return fmt.Errorf("scan: unknown type %q (use energy or active)", *scanType)
    }
    return nil
}
```

Note on `run()` dispatch: `scan` needs remaining args passed through for the sub-flagset:

```go
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
        return runScan(ctx, portPath, flag.Args()[1:]) // pass remaining args
    default:
        return fmt.Errorf("unknown command: %s", cmd)
    }
}
```

- [x] Add `network` command to `run()` switch and `flag.Usage`
- [x] Implement `runNetwork()` in `cmd/zigboo/main.go`
- [x] Add `scan` command to `run()` switch and `flag.Usage`, pass remaining args
- [x] Implement `runScan()` with `--type` flag using sub-FlagSet
- [x] Energy scan output: tabular channel + RSSI
- [x] Active scan output: tabular channel + PAN ID + extended PAN + stack profile + join status + LQI + RSSI

## Acceptance Criteria

### Functional Requirements

- [x] `client.NetworkState(ctx)` returns `EmberNetworkStatus` for all 5 enum values
- [x] `client.GetNetworkParameters(ctx)` returns `EmberNodeType` + `NetworkParameters` on success
- [x] `client.GetNetworkParameters(ctx)` returns error when EmberStatus != 0
- [x] `client.StartEnergyScan(ctx, mask, dur)` blocks until scan start confirmed, returns channels on success
- [x] `client.StartEnergyScan(ctx, mask, dur)` returns error immediately when scan start fails
- [x] Energy scan streams `EnergyScanResult` on result channel, closes on scanComplete
- [x] `client.StartActiveScan(ctx, mask, dur)` same contract with `NetworkScanResult`
- [x] `ash.Conn.Recv(ctx)` receives unsolicited DATA, sends ACK, advances only `ackNum`
- [x] `Command()` returns `ErrScanInProgress` during active scan
- [x] `scanning` flag cleared after scan completes or errors
- [x] `zigboo network` displays state and parameters (or just state when no network)
- [x] `zigboo scan --type energy` displays channel/RSSI table
- [x] `zigboo scan --type active` displays discovered networks table
- [x] Ctrl+C cancels scan cleanly (context propagated to goroutine)

### Quality Gates

- [x] `go test ./...` passes
- [x] `go vet ./...` clean
- [x] No goroutine leaks in scan tests (channels closed in all exit paths)
- [x] Table-driven tests with known byte sequences for all EZSP response parsing
- [x] Asymmetric test vectors (per learnings from ASH encoding bug)

## Dependencies & Risks

| Risk | Impact | Mitigation |
|------|--------|------------|
| `Recv()` races with `Send()` on frames channel | Data corruption, wrong frames delivered | Scan exclusivity flag prevents concurrent access |
| NCP never sends scanCompleteHandler | Goroutine hangs | Context cancellation (Ctrl+C) terminates goroutine; document that callers should use context deadlines |
| Channel buffer backpressure | Reader goroutine blocks | Result channel buffered to 16 (max Zigbee channels) |
| EmberNetworkParameters struct size varies by EZSP version | Parse failure on different firmware | Validate minimum 14 bytes, ignore trailing bytes |
| Unexpected callbacks during scan (e.g., stackStatusHandler) | Goroutine confusion | Log and skip frame IDs that aren't scan-related |

## References

### Internal References

- Brainstorm: `docs/brainstorms/2026-03-03-network-state-scan-brainstorm.md`
- ASH encoding bug postmortem: `docs/solutions/logic-errors/ash-data-frame-encoding-order-and-control-byte.md`
- Existing EZSP commands: `ezsp/ezsp.go:156-179` (GetNodeID, GetEUI64 patterns)
- ASH frame handling: `ash/ash.go:189-256` (waitForResponse pattern for Recv)
- CLI dispatch: `cmd/zigboo/main.go:68-79` (run switch)

### External References

- UG100: EZSP Reference Guide (frame IDs, command parameters, callback definitions)
- UG101: ASH Protocol Reference (DATA frame format, ACK/NAK semantics)
