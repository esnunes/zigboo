---
title: "feat: Device Joining and Pairing Flow"
type: feat
date: 2026-03-05
brainstorm: docs/brainstorms/2026-03-05-device-joining-brainstorm.md
---

# feat: Device Joining and Pairing Flow

## Overview

Implement an end-to-end device pairing flow for zigboo. A new `zigboo pair` command opens the network, listens for join events, interviews each device (ZDO descriptors + ZCL Basic attributes), persists device info to JSON, and streams progress to the terminal.

This requires foundational infrastructure: ASH connection refactoring, a `host/` package for callback dispatch, APS messaging, and new `zdo/` and `zcl/` protocol packages.

## Problem Statement

zigboo can form a Zigbee network and open it for joining, but has no way to detect or interact with devices that join. The `PermitJoining` command opens the door, but nobody is listening on the other side. Users need a single command that handles the full device onboarding lifecycle.

## Proposed Solution

Build the pairing flow in six phases, each delivering a testable increment:

1. **ASH connection refactoring** -- split internal frame routing so DATA payloads and ACK/NAK frames go to separate channels
2. **Host package + callback infrastructure** -- reader goroutine, command serialization, callback dispatch
3. **APS messaging** -- `sendUnicast`, `incomingMessageHandler`, endpoint registration
4. **ZDO package** -- node descriptor, active endpoints, simple descriptor requests
5. **ZCL package** -- frame construction, Read Attributes command for Basic cluster
6. **Device persistence + `zigboo pair` CLI** -- JSON storage, interactive pairing command

## Technical Approach

### Phase 1: ASH Connection Refactoring

**Goal:** Enable `host/` to own all EZSP payload routing by splitting `ash.Conn`'s internal frame delivery into separate channels for ACK/NAK frames and DATA payloads.

**Current state:** `ash.Conn` has a single `frames chan []byte` (cap 8) fed by the reader goroutine. Both `Send()` (via `waitForResponse()`) and `Recv()` consume from this channel. They cannot run concurrently.

**Changes to `ash/ash.go`:**

- Split `frames chan []byte` into two channels:
  - `acks chan ackFrame` -- ACK and NAK frames (consumed by `Send()` for retransmit logic)
  - `data chan []byte` -- DATA frame payloads (consumed by `host/`)
  - RSTACK frames signal on both channels (or a dedicated error mechanism)
- Modify the internal reader goroutine to classify decoded frames and route to the appropriate channel
- `Send(ctx, data)` changes: writes DATA frame to serial, then reads from `acks` for ACK confirmation. Handles NAK retransmit. Returns `error` only (no response payload). The EZSP response arrives separately via `data`
- `Recv(ctx)` stays for backward compatibility but reads from `data` channel. Existing scan code continues to work during migration
- New `Data() <-chan []byte` method exposes the `data` channel for `host/` to consume directly

**ACK handling stays in `ash.Conn`:** The ACK/NAK/retransmit protocol is an ASH-level concern. `host/` never sees ACK frames. It only receives decoded EZSP payloads from DATA frames via the `data` channel.

**Files:**

- `ash/ash.go` -- refactor `Conn` struct, reader goroutine, `Send()`, add `Data()`
- `ash/ash_test.go` -- update tests for new `Send()` behavior (returns error, not response)

**Acceptance criteria:**

- [ ] `Send()` returns after ACK, does not wait for response DATA
- [ ] DATA frame payloads are delivered via `Data()` channel
- [ ] ACK/NAK/retransmit logic unchanged (still in `ash.Conn`)
- [ ] RSTACK during `Send()` returns `ErrConnectionReset`
- [ ] Existing `Recv()` still works (reads from `data` channel) for scan backward compat
- [ ] All existing `ash/` tests pass (updated for new behavior)
- [ ] New tests verify ACK/DATA channel separation with interleaved frames

### Phase 2: Host Package + Callback Infrastructure

**Goal:** New `host/` package that owns EZSP session lifecycle, serializes command execution, and dispatches callbacks.

**Architecture:**

```
cmd/zigboo  -->  host/  -->  ezsp/  -->  ash/  -->  serial/
                  |
                  +--> zdo/
                  +--> zcl/
```

`host.Host` struct:

- Owns a goroutine that reads from `ash.Conn.Data()` continuously
- Classifies each EZSP frame as command response or callback (using the callback bit in the frame control byte, or frame ID matching against the pending command's expected response frame ID)
- Routes command responses to a per-command response channel
- Dispatches callbacks to registered handler functions
- Serializes all EZSP command execution (one command in flight at a time)

**Command execution flow:**

1. Caller calls `host.Command(ctx, frameID, params)`
2. `host/` acquires the command slot (only one at a time)
3. Encodes the EZSP frame (delegates to `ezsp/` encoding functions)
4. Calls `ash.Conn.Send(ctx, encodedFrame)` -- sends DATA, waits for ACK, returns
5. Reader goroutine receives response DATA from `ash.Conn.Data()`
6. Matches response frame ID to pending command, sends on response channel
7. Caller receives response, command slot released

**Callback dispatch:**

- Handlers registered via `host.OnCallback(frameID uint16, fn func(params []byte))`
- Unhandled callbacks logged at debug level via `slog` and discarded
- `stackStatusHandler` (0x0019) always registered internally for network state tracking

**EZSP frame encoding/decoding:**

- `ezsp/` gains exported `EncodeFrame` and `DecodeFrame` functions so `host/` can encode commands and decode responses without going through `ezsp.Client`
- `ezsp.Client` remains available for backward compatibility (existing scan and simple commands)

**Graceful shutdown:**

- `host.Host` has `Start(ctx)` and `Close()` lifecycle methods
- `Close()` cancels the reader goroutine, drains channels

**Files:**

- `host/host.go` -- `Host` struct, `New()`, `Start()`, `Close()`, `Command()`, `OnCallback()`
- `host/host_test.go` -- tests using the existing `mockPort` pattern (adapted for `host/`)
- `ezsp/frame.go` -- export `EncodeFrame()` and `DecodeFrame()` functions
- `ezsp/frame_test.go` -- tests for exported encoding functions

**Acceptance criteria:**

- [ ] `host.Command()` sends an EZSP command and returns the response
- [ ] Callbacks received between commands are dispatched to registered handlers
- [ ] Callbacks received during a pending command (between ACK and response) are dispatched correctly
- [ ] Only one command executes at a time (serialized)
- [ ] `Close()` cleanly shuts down the reader goroutine
- [ ] Unhandled callbacks are logged and discarded
- [ ] Tests verify: command-response, callback dispatch, interleaved callback-during-command, shutdown

### Phase 3: APS Messaging

**Goal:** Implement `sendUnicast` and `incomingMessageHandler` in `ezsp/`, expose them through `host/` for use by `zdo/` and `zcl/`.

**New EZSP commands in `ezsp/`:**

- `addEndpoint` (0x0002) -- register a local endpoint on the NCP. Required for ZCL communication (profile 0x0104, endpoint 1)
- `sendUnicast` (0x0034) -- send an APS unicast message. Parameters: type (direct=0x00), destination node ID (uint16), APS frame (profile, cluster, source EP, dest EP, options, group ID, sequence, payload)
- `messageSentHandler` (0x003F) -- callback confirming delivery. Contains message tag and status

**New callback in `host/`:**

- `incomingMessageHandler` (0x0045) -- callback for incoming APS messages. Contains: message type, APS frame (profile, cluster, source EP, dest EP, options, group ID, sequence), last hop LQI, last hop RSSI, sender node ID, binding index, address index, message payload

**Message tag management:**

- `host/` allocates monotonically increasing uint8 tags for `sendUnicast`
- `messageSentHandler` confirms delivery by matching tag
- For this milestone: fire-and-forget for ZDO requests (match responses by incoming message, not delivery confirmation). `messageSentHandler` logged but not awaited

**Incoming message routing in `host/`:**

- `host/` registers `incomingMessageHandler` as a callback
- Incoming messages are routed based on profile ID + cluster ID:
  - Profile 0x0000, cluster 0x8000-0x8FFF → ZDO responses → dispatched to `zdo/` handlers
  - Profile 0x0104 → ZCL messages → dispatched to `zcl/` handlers
- Routing done via registered message handlers: `host.OnMessage(profileID, clusterID, fn func(msg IncomingMessage))`

**Endpoint registration on startup:**

- `host.Start()` checks existing endpoints via `getEndpointCount`/`getEndpoint`
- If no HA profile endpoint exists, registers one via `addEndpoint`:
  - Endpoint 1, Profile 0x0104 (HA), Device ID 0x0005 (Configuration Tool)
  - Input clusters: Basic (0x0000)
  - Output clusters: Basic (0x0000), Identify (0x0003)

**Files:**

- `ezsp/ezsp.go` -- `AddEndpoint()`, `SendUnicast()` methods
- `ezsp/commands.go` -- new frame ID constants
- `ezsp/types.go` -- `EmberApsFrame` struct, `EmberOutgoingMessageType` enum
- `host/host.go` -- message tag allocation, `OnMessage()`, incoming message routing, endpoint registration in `Start()`
- `host/message.go` -- `IncomingMessage` type, message routing logic
- Tests for all new commands and routing

**Acceptance criteria:**

- [ ] `addEndpoint` registers a local endpoint on the NCP
- [ ] `sendUnicast` sends an APS unicast message with correct framing
- [ ] `incomingMessageHandler` callbacks are received and decoded
- [ ] Incoming messages are routed to profile/cluster handlers
- [ ] Endpoint auto-registered on `host.Start()` if not present
- [ ] Tests verify send/receive round-trip with mock NCP

### Phase 4: ZDO Package

**Goal:** New `zdo/` package implementing the ZDO commands needed for device interview.

**ZDO request/response pattern:**

- ZDO requests are APS unicast to destination endpoint 0, with cluster ID = ZDO command ID
- ZDO responses arrive as incoming messages with cluster ID = ZDO command ID + 0x8000
- Byte 0 of every ZDO frame is a transaction sequence number
- `zdo/` manages its own sequence number (uint8, monotonically increasing)

**ZDO commands:**

1. **Node Descriptor Request** (cluster 0x0002) → Response (cluster 0x8002)
   - Request: `[seq, targetAddr_lo, targetAddr_hi]`
   - Response: `[seq, status, targetAddr(2), nodeDescriptor(13)]`
   - Node descriptor contains: logical type (coordinator/router/end device), complex descriptor available, user descriptor available, APS flags, frequency band, MAC capability flags, manufacturer code, max buffer size, max incoming/outgoing transfer size, server mask, max outgoing transfer size, descriptor capability

2. **Active Endpoints Request** (cluster 0x0005) → Response (cluster 0x8005)
   - Request: `[seq, targetAddr_lo, targetAddr_hi]`
   - Response: `[seq, status, targetAddr(2), endpointCount, endpoint1, endpoint2, ...]`

3. **Simple Descriptor Request** (cluster 0x0004) → Response (cluster 0x8004)
   - Request: `[seq, targetAddr_lo, targetAddr_hi, endpoint]`
   - Response: `[seq, status, targetAddr(2), length, simpleDescriptor...]`
   - Simple descriptor contains: endpoint, profile ID, device ID, device version, input cluster count, input cluster IDs, output cluster count, output cluster IDs

**Response correlation:**

- `zdo/` sends a request via `host.SendUnicast()` (wrapper around the EZSP command)
- Registers an expected response filter with `host/` by source address + response cluster ID
- Waits on a channel for the matching response with a configurable timeout (default 5 seconds)
- On timeout: returns error (caller decides whether to retry or skip)

**`zdo.Client` struct:**

```
type Client struct {
    host *host.Host  // for sending and receiving
    seq  uint8       // ZDO transaction sequence number
}
```

**Files:**

- `zdo/zdo.go` -- `Client`, `New()`, `NodeDescriptor()`, `ActiveEndpoints()`, `SimpleDescriptor()`
- `zdo/types.go` -- `NodeDescriptor`, `SimpleDescriptor`, `Endpoint` types
- `zdo/zdo_test.go` -- tests with mock host

**Acceptance criteria:**

- [ ] `NodeDescriptor(ctx, addr)` returns parsed node descriptor
- [ ] `ActiveEndpoints(ctx, addr)` returns list of active endpoint numbers
- [ ] `SimpleDescriptor(ctx, addr, ep)` returns parsed simple descriptor
- [ ] Responses correctly correlated by source address + cluster ID + sequence number
- [ ] Timeout returns error (not hang)
- [ ] Tests verify each command with mock responses, error cases, and timeout

### Phase 5: ZCL Package

**Goal:** New `zcl/` package implementing enough ZCL to read Basic cluster attributes.

**ZCL frame structure (foundation frame):**

- Frame control (1 byte): frame type (0=global), direction (0=client-to-server), disable default response
- Sequence number (1 byte): managed by `zcl/`
- Command ID (1 byte): 0x00 = Read Attributes

**Read Attributes command:**

- Command ID: 0x00
- Payload: list of attribute IDs (uint16 LE)
- Response command ID: 0x01 (Read Attributes Response)
- Response payload: per attribute: `[attrID(2), status(1), dataType(1), value(...)]`

**Basic cluster attributes to read:**

| Attribute ID | Name | Type | ZCL Data Type |
|---|---|---|---|
| 0x0004 | Manufacturer Name | string | 0x42 (CharString) |
| 0x0005 | Model Identifier | string | 0x42 (CharString) |
| 0x4000 | SW Build ID | string | 0x42 (CharString) |

**ZCL data type decoding:**

- `0x42` CharString: `[length(1), chars...]`
- Other types can be added incrementally

**Response correlation:**

- Similar to ZDO: send via `host.SendUnicast()`, register expected response filter by source address + cluster ID (Basic = 0x0000) + ZCL sequence number
- Wait on channel with timeout

**`zcl.Client` struct:**

```
type Client struct {
    host *host.Host
    seq  uint8  // ZCL sequence number
}
```

**Files:**

- `zcl/zcl.go` -- `Client`, `New()`, `ReadAttributes()`
- `zcl/frame.go` -- ZCL frame encoding/decoding
- `zcl/types.go` -- `AttributeValue`, ZCL data type constants
- `zcl/zcl_test.go` -- tests with mock responses

**Acceptance criteria:**

- [ ] `ReadAttributes(ctx, addr, ep, clusterID, attrIDs)` returns attribute values
- [ ] ZCL frame correctly encoded (frame control, seq, command ID, attribute IDs)
- [ ] CharString data type decoded correctly
- [ ] Unsupported attribute status (0x86) handled gracefully (nil value, no error)
- [ ] Timeout returns error
- [ ] Tests verify encoding, decoding, partial success, and timeout

### Phase 6: Device Persistence + `zigboo pair` CLI

**Goal:** JSON device storage and the interactive `zigboo pair` command.

**Device JSON schema:**

File path: `devices.json` in the current working directory (configurable via `--devices-file` flag later).

```json
{
  "00:11:22:33:44:55:66:77": {
    "ieee": "00:11:22:33:44:55:66:77",
    "nwkAddr": "0x1A2B",
    "nodeType": "end-device",
    "manufacturer": "LUMI",
    "model": "lumi.sensor_motion.aq2",
    "firmware": "3.0.0_0034",
    "interviewComplete": true,
    "lastSeen": "2026-03-05T12:34:56Z",
    "endpoints": [
      {
        "id": 1,
        "profileId": "0x0104",
        "deviceId": "0x0107",
        "inClusters": ["0x0000", "0x0001", "0x0406"],
        "outClusters": ["0x0003"]
      }
    ]
  }
}
```

Key by EUI-64 (IEEE address) for deduplication. On rejoin (same EUI-64, possibly new network address), re-interview and update the record.

**Device store (`host/device.go`):**

- `DeviceStore` struct with in-memory map + JSON file path
- `Load()` -- read JSON file, populate map. File not existing is OK (empty map)
- `Save(device)` -- update in-memory map, write entire JSON file atomically (write temp + rename)
- `All()` -- return all devices
- `Get(ieee)` -- return single device

**Interview sequence (`host/interview.go`):**

1. Receive `trustCenterJoinHandler` callback → extract EUI-64 and network address
2. Node Descriptor Request (ZDO) → extract node type, MAC capabilities
3. Active Endpoints Request (ZDO) → extract endpoint list
4. Simple Descriptor Request (ZDO) per endpoint → extract clusters
5. Read Basic Cluster Attributes (ZCL) on endpoint 1 (or first HA endpoint) → manufacturer, model, firmware
6. Persist device to store

**Partial interview handling:**

- Each step that succeeds populates the device struct
- If a step fails (timeout, error), log a warning, set `interviewComplete: false`, and continue to the next step where possible
- ZCL read failure is non-fatal (common with sleepy devices) -- persist what was gathered from ZDO
- Device is saved even with `interviewComplete: false`

**Sleepy end device note:**

- Detected via Node Descriptor MAC capabilities (bit 3 = receiver-on-when-idle)
- First version: uses standard timeouts (5s per ZDO/ZCL request). Sleepy devices may fail interview if they go to sleep. This is documented as a known limitation; future improvement: extend timeouts or retry on next wake.

**Concurrent join handling:**

- Interviews are serialized. Join events are queued in a buffered channel (cap 16)
- Each interview runs to completion (or timeout) before the next starts
- This avoids `ash.Conn.Send()` concurrency issues and keeps the implementation simple

**`zigboo pair` command (`cmd/zigboo/main.go`):**

```
zigboo pair [--duration N]
```

- `--duration`: permit-join duration in seconds (default 120, range 1-254)
- Duration 255 allowed with warning: "Network will remain open indefinitely until explicitly closed"
- Duration 0 rejected: "Duration must be at least 1 second"

**Command flow:**

1. Open connection via `resetAndNegotiate()` + `NegotiateVersion()`
2. Create `host.Host`, call `Start(ctx)` (starts reader goroutine, registers HA endpoint)
3. Check network state via `host.Command()` → `NetworkState`. Error if not joined: "No active network. Run 'zigboo network init' first."
4. Call `PermitJoining(ctx, duration)` via `host.Command()`
5. Print "Network open for joining (Ns remaining)..."
6. Register `trustCenterJoinHandler` callback → queue join events
7. Loop: dequeue join events, run interview, print results, save to JSON
8. On timeout or context cancellation: cleanup
9. Print summary: "N device(s) paired"

**Graceful shutdown (Ctrl+C):**

- Main context cancelled via `signal.NotifyContext`
- Cleanup uses a separate `context.WithTimeout(context.Background(), 5*time.Second)`:
  1. Close permit-join: `PermitJoining(cleanupCtx, 0)`
  2. Print "Network closed for joining."
  3. `host.Close()`
  4. `conn.Close()`, `port.Close()`

**Terminal output (static, no cursor manipulation):**

```
$ zigboo pair --duration 120
Network open for joining (120s remaining)...

[12:34:56] Device joined: 0x1A2B (EUI64: 00:11:22:33:44:55:66:77)
  Interviewing...
  Type: End Device
  Manufacturer: LUMI
  Model: lumi.sensor_motion.aq2
  Endpoints: 1
    EP 1: Profile 0x0104, Device 0x0107
      In:  0x0000 (Basic), 0x0001 (PowerConfiguration), 0x0406 (OccupancySensing)
      Out: 0x0003 (Identify)
  Device saved.

Waiting for more devices...
^C
Network closed for joining.
1 device(s) paired.
```

**Files:**

- `host/device.go` -- `Device` struct, `DeviceStore`, `Load()`, `Save()`, `All()`, `Get()`
- `host/interview.go` -- `Interview(ctx, eui64, nwkAddr)` method on `Host`
- `host/device_test.go` -- store tests (load, save, update, corrupt file)
- `host/interview_test.go` -- interview tests (full success, partial failure, timeout)
- `cmd/zigboo/main.go` -- `runPair()` function, `pair` subcommand dispatch

**Acceptance criteria:**

- [ ] `zigboo pair` opens permit-join and streams join events
- [ ] Device interview runs automatically on join (node descriptor + endpoints + simple descriptor + ZCL Basic)
- [ ] Partial interview failure saves what was gathered with `interviewComplete: false`
- [ ] Device info persisted to `devices.json` keyed by EUI-64
- [ ] Rejoin (same EUI-64) updates existing record
- [ ] Ctrl+C closes permit-join via separate cleanup context
- [ ] Duration validated (1-254 normal, 255 with warning, 0 rejected)
- [ ] "No active network" error if network not formed
- [ ] Multiple join events are queued and processed sequentially
- [ ] Tests for: happy path, no devices, partial interview, rejoin, Ctrl+C cleanup

## Acceptance Criteria

### Functional Requirements

- [ ] `zigboo pair --duration 120` opens network, detects joins, interviews devices, persists to JSON
- [ ] Device interview captures: node type, manufacturer, model, firmware, endpoints, clusters
- [ ] `devices.json` created/updated atomically with correct schema
- [ ] Graceful Ctrl+C closes permit-join before exiting
- [ ] Existing commands (`network state`, `scan`, `config`, etc.) continue to work unchanged

### Non-Functional Requirements

- [ ] No ASH connection refactoring breaks existing scan functionality
- [ ] All new packages have doc comments on exported symbols
- [ ] Error messages follow existing wrapping convention (`fmt.Errorf("pkg: method: %w", err)`)

### Quality Gates

- [ ] All new code has tests following existing patterns (table-driven, asymmetric values, external reference where applicable)
- [ ] `go vet ./...` passes
- [ ] `go test ./...` passes
- [ ] No race conditions (`go test -race ./...`)

## Design Decisions

| Decision | Choice | Alternatives Considered |
|---|---|---|
| ASH refactoring approach | Split `frames` channel into `acks` + `data` channels | (a) `host/` takes over raw frame reading from serial (moves too much ASH logic up), (b) Add `SendOnly()` without channel split (ACK routing unclear) |
| Command serialization | Single command in flight, serialized by `host/` | (a) Command queue with multiplexing (over-engineered), (b) Mutex in `ezsp.Client` (current approach, doesn't handle callbacks) |
| Interview concurrency | Serialized, join events queued (cap 16) | (a) Parallel interviews (requires concurrent `ash.Conn.Send()`, risky), (b) Drop events beyond queue (data loss) |
| Response correlation | Per-package sequence numbers (`zdo/`, `zcl/`), matched by source + cluster + seq | (a) Centralized correlation in `host/` (couples host to protocol details), (b) Global sequence counter (collision between ZDO and ZCL) |
| Partial interview | Save what succeeded, set `interviewComplete: false` | (a) All-or-nothing (loses data for sleepy devices), (b) Automatic retry (complex, may never succeed for sleepy devices) |
| Existing command migration | Not migrated in this milestone, use `ezsp.Client` directly | (a) Migrate all commands to `host/` now (scope explosion), (b) Deprecate `ezsp.Client` (premature) |
| Countdown timer | Static messages at events, no live updating | (a) Live countdown with ticker + ANSI cursor (complex), (b) Progress bar library (dependency) |

## Dependencies & Risks

| Risk | Impact | Mitigation |
|---|---|---|
| ASH `Send()` refactoring breaks existing commands | High -- all EZSP communication affected | Phase 1 is self-contained with full test coverage. Existing `Recv()` preserved for backward compat. Run all tests after refactoring. |
| Sleepy end devices fail interview | Medium -- majority of battery sensors | Document as known limitation. Save partial data. Phase 2 improvement: extended timeouts, retry on wake. |
| NCP trust center policy misconfigured | High -- devices join but callbacks don't fire | Test with real hardware early in Phase 2. Verify `setInitialSecurityState` bitmask enables trust center join callbacks. |
| Frame ID classification (response vs callback) incorrect | High -- commands hang or callbacks lost | Use EZSP frame control callback bit (bit 4 in extended format) as primary classifier. Fall back to frame ID matching against pending command. |
| `ash.Conn` channel split introduces deadlock | Medium -- blocked channels stall communication | Buffered channels (acks: 4, data: 8). Timeouts on all channel operations. Deadlock detection in tests via `-race` and explicit timeout assertions. |
| Coordinator endpoint not registered → ZCL fails | Medium -- interview step 5 silently fails | `host.Start()` auto-registers HA endpoint. Verified in Phase 3 acceptance criteria. |

## References

### Internal References

- Brainstorm: `docs/brainstorms/2026-03-05-device-joining-brainstorm.md`
- ASH connection: `ash/ash.go` (Send: line 159, Recv: line 270, reader: line 321)
- EZSP client: `ezsp/ezsp.go` (Command: line 137, scan model: line 542)
- CLI patterns: `cmd/zigboo/main.go` (resetAndNegotiate: line 143)
- Test patterns: `ezsp/ezsp_test.go` (setupMockNCP: line 169)
- Existing plans: `docs/plans/2026-03-01-feat-ash-ezsp-serial-communication-plan.md`
- ASH encoding postmortem: `docs/solutions/logic-errors/ash-data-frame-encoding-order-and-control-byte.md`

### EZSP Frame IDs (New)

| Frame ID | Name | Type |
|---|---|---|
| 0x0002 | addEndpoint | Command |
| 0x0023 | childJoinHandler | Callback |
| 0x0024 | trustCenterJoinHandler | Callback |
| 0x0034 | sendUnicast | Command |
| 0x003F | messageSentHandler | Callback |
| 0x0045 | incomingMessageHandler | Callback |

### ZDO Cluster IDs

| Request | Response | Name |
|---|---|---|
| 0x0002 | 0x8002 | Node Descriptor |
| 0x0004 | 0x8004 | Simple Descriptor |
| 0x0005 | 0x8005 | Active Endpoints |

### Known Limitations (First Version)

- Sleepy end devices may fail ZCL interview (saved as `interviewComplete: false`)
- No install-code provisioning (well-known HA key only)
- No device removal command (manual JSON editing)
- No live countdown timer (static event messages)
- Existing CLI commands not migrated to `host/` (still use `ezsp.Client` directly)
