# Brainstorm: networkState, getNetworkParameters, startScan

**Date:** 2026-03-03
**Status:** Approved
**Milestone:** 2 (NCP Configuration & Network Formation) + partial Milestone 3 (Callback Processing)

## What We're Building

Three EZSP commands that give visibility into the NCP's network state and RF environment:

1. **`networkState`** (EZSP 0x0018) — query whether the NCP is up, down, or joining. Simple request/response, returns a 1-byte `EmberNetworkStatus` enum.

2. **`getNetworkParameters`** (EZSP 0x0028) — read the current PAN ID, channel, extended PAN ID, and node type. Simple request/response, returns `EmberStatus` + `EmberNodeType` + `EmberNetworkParameters` struct.

3. **`startScan`** (EZSP 0x001A) — initiate energy or active scan. This is the first async command: the NCP sends multiple callback frames (one per channel/network) followed by `scanCompleteHandler`.

## Why This Approach

### Simple commands (networkState, getNetworkParameters)

These follow the exact same pattern as `GetNodeID`/`GetEUI64`:
- Add frame ID constant to `commands.go`
- Add method on `*Client` that calls `c.Command()`, validates response length, deserializes
- No architectural changes needed

### Async command (startScan)

`startScan` is fundamentally different from previous commands. After the initial response (status byte), the NCP sends unsolicited DATA frames:

- **Energy scan:** `energyScanResultHandler` (0x0048) per channel — contains channel number + max RSSI
- **Active scan:** `networkFoundHandler` (0x001B) per discovered network — contains network params + LQI + RSSI
- **Both:** `scanCompleteHandler` (0x001C) when done — contains channel + status

This requires two new capabilities:
1. **ASH layer:** ability to receive unsolicited DATA frames (new `Recv()` method)
2. **EZSP layer:** ability to decode callback frame IDs and stream results

## Key Decisions

1. **Goroutines + channels for concurrency** (prior decision from Milestone 1 brainstorm) — dedicated reader goroutine at ASH level, EZSP demultiplexes callback frames from command responses.

2. **Channel-based streaming API for scans** — `StartScan()` returns a channel that emits results as they arrive. Channel closes when `scanCompleteHandler` arrives. Idiomatic Go for streaming data.

3. **New `ash.Conn.Recv()` method** — reads the next DATA frame from the channel, sends ACK, returns payload. Minimal ASH layer change. `StartScan` calls it in a loop after the initial `Command()`.

4. **CLI: `network` subcommand** — new `zigboo network` command shows network state + parameters. Keeps `info` for device identity only.

5. **CLI: `scan --type energy|active`** — single `scan` subcommand with `--type` flag. Defaults to `energy`. Both scan types implemented.

## Design Details

### ash.Conn.Recv()

```
func (c *Conn) Recv(ctx context.Context) ([]byte, error)
```

Reads the next DATA frame from `c.frames`, sends ACK, advances `ackNum`, returns the EZSP payload. Uses `retransmitTimeout` as a deadline. Handles ACK/NAK/RSTACK the same as `waitForResponse` but without an outgoing frame.

### EZSP StartScan API

```
type EnergyScanResult struct {
    Channel  uint8
    MaxRSSI  int8
}

type NetworkScanResult struct {
    Channel        uint8
    PanID          uint16
    ExtendedPanID  [8]byte
    AllowingJoin   bool
    StackProfile   uint8
    NwkUpdateID    uint8
    LQI            uint8
    RSSI           int8
}

func (c *Client) StartEnergyScan(ctx context.Context, channelMask uint32, duration uint8) (<-chan EnergyScanResult, <-chan error, error)
func (c *Client) StartActiveScan(ctx context.Context, channelMask uint32, duration uint8) (<-chan NetworkScanResult, <-chan error, error)
```

Each method:
1. Sends `startScan` command, **blocks** until status response arrives
2. If status != success, returns `nil, nil, error` immediately (no goroutine spawned)
3. On success, spawns goroutine that calls `c.conn.Recv()` in a loop
4. Decodes EZSP frame ID from each callback frame
5. Sends typed result on the result channel
6. On `scanCompleteHandler`: closes result channel, sends any error on error channel, then closes error channel

### EmberNetworkStatus enum

```
const (
    NetworkStatusNoNetwork    = 0x00
    NetworkStatusJoiningNetwork = 0x01
    NetworkStatusJoinedNetwork  = 0x02
    NetworkStatusJoinedNoParent = 0x03
    NetworkStatusLeavingNetwork = 0x04
)
```

### EmberNetworkParameters struct

```
type NetworkParameters struct {
    ExtendedPanID [8]byte
    PanID         uint16
    RadioTxPower  int8
    RadioChannel  uint8
}
```

### CLI output format

**`zigboo network`:**
```
Network state: joined
PAN ID:        0x1A2B
Extended PAN:  01:23:45:67:89:AB:CD:EF
Channel:       15
TX power:      8 dBm
Node type:     coordinator
```

**`zigboo scan --type energy`:**
```
Channel  RSSI
     11  -87 dBm
     12  -92 dBm
     ...
     26  -95 dBm
```

**`zigboo scan --type active`:**
```
Channel  PAN ID  Extended PAN ID              Stack  Join  LQI  RSSI
     15  0x1A2B  01:23:45:67:89:AB:CD:EF      2      yes  255  -45 dBm
     20  0x3C4D  FE:DC:BA:98:76:54:32:10      2      no   180  -62 dBm
```

## Scope Boundaries

**In scope:**
- `networkState`, `getNetworkParameters`, `startScan` EZSP commands
- `ash.Conn.Recv()` method
- EZSP callback frame ID decoding (for scan callbacks only)
- `network` and `scan` CLI subcommands
- Unit tests with mock port

**Out of scope:**
- General callback polling infrastructure (Milestone 3)
- `formNetwork`, `networkInit` (other Milestone 2 items)
- Any NCP configuration (`setConfigurationValue`, etc.)
