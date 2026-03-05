---
title: "feat: Add network lifecycle commands (init, form, security, permit-join)"
type: feat
date: 2026-03-03
brainstorm: docs/brainstorms/2026-03-03-network-commands-brainstorm.md
---

# feat: Add network lifecycle commands

## Overview

Implement four EZSP commands (`networkInit`, `formNetwork`, `setInitialSecurityState`, `permitJoining`) and restructure the CLI `network` command into a subcommand group with `init`, `state`, and `permit-join` subcommands.

## Problem Statement

The coordinator can query network state and scan for networks, but cannot form or join a Zigbee network. These commands are the minimum required to bring a Zigbee coordinator online and allow devices to join.

## Proposed Solution

Add the four EZSP client methods following established patterns, then build a CLI orchestrator (`network init`) that chains them into a single "make network ready" command.

## Technical Approach

### Phase 1: EZSP Types and Constants

**Files:** `ezsp/commands.go`, `ezsp/types.go`

Add frame ID constants to `ezsp/commands.go`:

```go
// networkInit (0x0017)
frameIDNetworkInit = 0x0017
// formNetwork (0x001E)
frameIDFormNetwork = 0x001E
// permitJoining (0x0022)
frameIDPermitJoining = 0x0022
// setInitialSecurityState (0x0068)
frameIDSetInitialSecurityState = 0x0068
```

Add types to `ezsp/types.go`:

- `EmberInitialSecurityBitmask` (`uint16`) with constants:
  - `EmberHavePreconfiguredKey = 0x0100`
  - `EmberHaveNetworkKey = 0x0200`
  - `EmberRequireEncryptedKey = 0x0800`
  - `EmberTrustCenterGlobalLinkKey = 0x0004`
  - `EmberHaveTrustCenterEUI64 = 0x0040`
- `EmberNetworkInitBitmask` (`uint16`) with:
  - `EmberNetworkInitNoOptions = 0x0000`
- `EmberInitialSecurityState` struct:
  - `Bitmask EmberInitialSecurityBitmask`
  - `PreconfiguredKey [16]byte`
  - `NetworkKey [16]byte`
  - `NetworkKeySequenceNumber uint8`
  - `PreconfiguredTrustCenterEUI64 [8]byte`

Add the well-known HA link key as a package-level variable:

```go
// ZigbeeHALinkKey is the well-known "ZigBeeAlliance09" trust center link key
// used by Zigbee Home Automation networks.
var ZigbeeHALinkKey = [16]byte{
    0x5A, 0x69, 0x67, 0x42, 0x65, 0x65, 0x41, 0x6C,
    0x6C, 0x69, 0x61, 0x6E, 0x63, 0x65, 0x30, 0x39,
}
```

### Phase 2: EZSP Client Methods

**File:** `ezsp/ezsp.go`

#### `NetworkInit`

```go
func (c *Client) NetworkInit(ctx context.Context) error
```

- Sends `frameIDNetworkInit` with 2-byte `EmberNetworkInitBitmask` parameter (`0x0000` little-endian) for EZSP v9+
- **Critical:** EZSP v9+ requires the 2-byte bitmask parameter. The NCP runs EZSP v13. Sending zero-length params would cause a frame parse error on the NCP.
- Response: 1 byte `EmberStatus`. Returns `nil` on success (0x00), error otherwise.

#### `FormNetwork`

```go
func (c *Client) FormNetwork(ctx context.Context, params NetworkParameters) error
```

- Encodes `NetworkParameters` as 12 bytes: `ExtendedPanID[8] + PanID(2 LE) + RadioTxPower(1) + RadioChannel(1)`
- Reuses the existing `NetworkParameters` struct from `types.go`
- Response: 1 byte `EmberStatus`

#### `SetInitialSecurityState`

```go
func (c *Client) SetInitialSecurityState(ctx context.Context, state EmberInitialSecurityState) error
```

- Encodes `EmberInitialSecurityState` as 43 bytes: `Bitmask(2 LE) + PreconfiguredKey(16) + NetworkKey(16) + KeySequenceNumber(1) + TrustCenterEUI64(8)`
- Response: 1 byte `EzspStatus` (note: EzspStatus, not EmberStatus)
- **Network key must be generated with `crypto/rand`** when called from the orchestrator

#### `PermitJoining`

```go
func (c *Client) PermitJoining(ctx context.Context, duration uint8) error
```

- Sends 1-byte `duration` parameter (0=close, 1-254=seconds, 255=indefinite)
- Response: 1 byte `EmberStatus`

### Phase 3: Tests

**File:** `ezsp/ezsp_test.go`

For each command, follow the established test pattern with `setupMockNCP`:

| Test | Description |
|------|-------------|
| `TestNetworkInit` | Happy path: EmberStatus success |
| `TestNetworkInit_EmberStatusError` | Non-zero EmberStatus (e.g., 0x90 not joined) |
| `TestNetworkInit_ResponseTooShort` | Empty response |
| `TestFormNetwork` | Happy path: verify 12-byte param encoding |
| `TestFormNetwork_EmberStatusError` | Non-zero EmberStatus |
| `TestFormNetwork_ResponseTooShort` | Truncated response |
| `TestSetInitialSecurityState` | Happy path: verify 43-byte param encoding |
| `TestSetInitialSecurityState_EzspStatusError` | Non-zero EzspStatus |
| `TestSetInitialSecurityState_ResponseTooShort` | Truncated response |
| `TestPermitJoining` | Happy path with duration=60 |
| `TestPermitJoining_EmberStatusError` | Non-zero EmberStatus |
| `TestPermitJoining_ResponseTooShort` | Truncated response |

**Testing lessons from `docs/solutions/`:**
- Use asymmetric, non-zero test values in every field to detect byte-swap bugs
- Verify the full serialized byte sequence (not just round-trip), matching expected wire format
- For `FormNetwork` params encoding: construct expected bytes manually and compare against what `Command()` receives

### Phase 4: CLI Restructuring

**File:** `cmd/zigboo/main.go`

#### 4a: Convert `network` to subcommand group

Change `runNetwork` signature from `(ctx, portPath)` to `(ctx, portPath, args)`. Dispatch subcommands via positional arg (following the `config` pattern):

```
zigboo network init [--channel N] [--pan-id N] [--tx-power N]
zigboo network state
zigboo network permit-join [--duration N]
```

Bare `zigboo network` (no subcommand) defaults to `network state` for backward compatibility.

#### 4b: `network state` subcommand

Moves the existing `runNetwork` body into `runNetworkState`. Shows network status and, if joined, network parameters. This is exactly the current `zigboo network` behavior.

#### 4c: `network init` subcommand

Orchestrator flow:

1. Check `NetworkState()` — if already `NetworkStatusJoinedNetwork`, print state and exit (do NOT call `setInitialSecurityState` on an active network)
2. Call `SetInitialSecurityState` with HA defaults: bitmask `0x0344` (`HavePreconfiguredKey | HaveNetworkKey | TrustCenterGlobalLinkKey | HaveTrustCenterEUI64`), preconfigured key = `ZigbeeHALinkKey`, network key = 16 bytes from `crypto/rand`, trust center EUI-64 = all zeros (NCP uses own)
3. Call `NetworkInit()` to try resuming stored network
4. If `NetworkInit` returns error, call `FormNetwork()` with params from flags/defaults
5. Call `NetworkState()` + `GetNetworkParameters()` to display final state
6. Print "Network resumed" or "Network formed" to distinguish paths

Flags with defaults:
- `--channel` default: 11
- `--pan-id` default: 0xFFFF (NCP auto-selects)
- `--tx-power` default: 8

Extended PAN ID: all zeros (NCP auto-generates). No CLI flag for now.

#### 4d: `network permit-join` subcommand

Flags:
- `--duration` default: 60 (uint8, 0-255)

Print confirmation: `"Permit joining: open for %d seconds\n"` or `"Permit joining: closed\n"` for duration 0.

**Descoped:** `--broadcast` flag. Broadcasting permit-join requires ZDO `Mgmt_Permit_Joining_req` via `sendBroadcast` (EZSP 0x0036), which is out of scope. Only local coordinator permit-join for this iteration.

#### 4e: Update usage text

Update `flag.Usage` to reflect new `network` subcommand group.

## Acceptance Criteria

### Functional Requirements

- [x] `networkInit` EZSP command works with EZSP v9+ (sends 2-byte bitmask)
- [x] `formNetwork` correctly serializes 12-byte `EmberNetworkParameters`
- [x] `setInitialSecurityState` correctly serializes 43-byte security state
- [x] `permitJoining` accepts duration 0-255
- [x] `zigboo network init` forms a new network on first run
- [x] `zigboo network init` resumes stored network on subsequent runs
- [x] `zigboo network init` skips modification if already joined
- [x] `zigboo network state` displays status and parameters
- [x] `zigboo network permit-join --duration 60` opens joining
- [x] `zigboo network permit-join --duration 0` closes joining
- [x] Bare `zigboo network` defaults to `network state`
- [x] Network key generated with `crypto/rand`

### Quality Gates

- [x] All existing tests pass (`go test ./...`)
- [x] All new EZSP methods have happy-path + error tests
- [x] `go vet ./...` passes
- [x] Test values use asymmetric non-zero values (per `docs/solutions/` learnings)

## Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| `networkInit` params | 2-byte bitmask on v9+ | NCP runs EZSP v13; zero-length params would fail |
| `--broadcast` | Descoped | Requires `sendBroadcast` EZSP command not yet implemented |
| Security bitmask | `0x0344` | Standard HA trust center: global link key + have TC EUI-64 + preconfigured key + network key |
| Trust center EUI-64 | All zeros | NCP interprets as "self" |
| Random network key | `crypto/rand` | Cryptographically secure, required for network security |
| Bare `network` | Defaults to `network state` | Backward compatibility |
| `network params` | Not added | Would overlap with `network state`; unnecessary |
| Extended PAN ID default | All zeros | NCP auto-generates; no CLI flag needed now |
| Already-joined guard | Check state first, skip if joined | Prevents corrupting active network security material |

## Dependencies & Risks

| Risk | Mitigation |
|------|------------|
| `networkInit` param format varies by EZSP version | Always send 2-byte bitmask since we negotiate v9+ |
| Security bitmask wrong for HA mode | Cross-reference with zigbee2mqtt ember adapter |
| `formNetwork` byte order mismatch | Test serialized bytes against expected wire format |
| ASH disconnect during multi-step init | Let error propagate; user re-runs `network init` which resumes via `networkInit` |
| `setInitialSecurityState` on active network | Guard with `NetworkState()` check at top of orchestrator |

## References

- Brainstorm: `docs/brainstorms/2026-03-03-network-commands-brainstorm.md`
- Existing command patterns: `ezsp/ezsp.go:180-260` (NetworkState, GetNetworkParameters, scan methods)
- CLI subcommand pattern: `cmd/zigboo/main.go:370-420` (config subcommands)
- Test infrastructure: `ezsp/ezsp_test.go:169-247` (setupMockNCP)
- Frame encoding lessons: `docs/solutions/logic-errors/ash-data-frame-encoding-order-and-control-byte.md`
- TODO.md milestones 2, 4, 5
