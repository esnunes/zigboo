---
title: "feat: ASH/EZSP serial communication with ZBDongle-E"
type: feat
date: 2026-03-01
brainstorm: docs/brainstorms/2026-03-01-zigbee-serial-communication-brainstorm.md
---

# feat: ASH/EZSP Serial Communication with ZBDongle-E

## Enhancement Summary

**Deepened on:** 2026-03-01
**Research agents used:** architecture-strategist, performance-oracle, security-sentinel, code-simplicity-reviewer, pattern-recognition-specialist, best-practices-researcher (Go serial, ASH/EZSP implementation)

### Key Improvements

1. **Protocol correctness fixes** — corrected ASH control byte values (RST=0xC0, RSTACK=0xC1, ERROR=0xC2), added missing reserved byte (0x18 CANCEL), and clarified data randomization ordering
2. **Concrete test vectors** — added known-good byte sequences from bellows for RST, RSTACK, and DATA frames to enable table-driven testing
3. **Hardened serial port open** — added `O_NOCTTY | O_CLOEXEC` flags and `sync.WaitGroup`-based shutdown
4. **Simplified scope** — removed `network-params` and EZSP callback/overflow handling from milestone 1; these are unnecessary for the first integration proof
5. **Architecture refinements** — added `context.Context` to `Send()` signature, expanded sentinel errors, clarified fd ownership and goroutine lifecycle

### Corrections from Original Plan

| Original | Corrected | Source |
|----------|-----------|--------|
| RST control byte 0x1A | 0xC0 | UG101 Table 3 |
| RSTACK control byte 0x1B | 0xC1 | UG101 Table 3 |
| ERROR control byte 0x1C | 0xC2 | UG101 Table 3 |
| 5 reserved bytes | 6 reserved bytes (added 0x18 CANCEL) | UG101 Section 4.2 |
| Randomization "applied after CRC" | Randomization applied to data+CRC bytes after CRC computation (randomize everything after control byte) | bellows source, UG101 Section 4.3 |

## Overview

Implement the foundational protocol stack for communicating with a Sonoff ZBDongle-E (Silicon Labs EFR32MG21) via serial UART. This is the first milestone of zigboo — a zigbee2mqtt replacement in Go.

The deliverable is a working CLI tool that can connect to the dongle, perform ASH reset, negotiate EZSP protocol version, and query basic information (network parameters, node ID).

## Problem Statement

There is no mature Go library for the EZSP/ASH protocol used by Silicon Labs Zigbee coordinators. To build a Go-based zigbee2mqtt replacement, we need to implement the protocol stack from scratch: serial port handling, ASH framing (transport), and EZSP commands (application).

## Proposed Solution

Build three library packages (`serial`, `ash`, `ezsp`) and a CLI binary (`cmd/zigboo`), following an integration-first approach — get each layer minimally working with real hardware before hardening.

## Technical Approach

### Architecture

```
cmd/zigboo/main.go          CLI entry point (flag-based, log/slog)
  |
ezsp/                       EZSP command layer
  ezsp.go                   Client type, version negotiation, command dispatch
  frame.go                  Frame encoding/decoding (legacy + extended formats)
  commands.go               Command ID constants and parameter types
  ezsp_test.go
  |
ash/                        ASH transport layer
  ash.go                    Connection type, reset handshake, DATA/ACK exchange
  frame.go                  Frame types, encoding, CRC, byte stuffing, randomization
  ash_test.go
  frame_test.go
  |
serial/                     Serial port abstraction
  port.go                   Port interface definition
  unix.go                   Unix termios implementation (build-tagged)
  serial_test.go
```

Module path: `github.com/esnunes/zigboo`

### Key Interfaces

```go
// serial/port.go

// Port provides access to a serial communication port.
type Port interface {
    io.ReadWriteCloser
    Flush() error // discard unread input (tcflush TCIFLUSH)
}

// Config holds serial port configuration.
type Config struct {
    Path     string
    BaudRate int // default: 115200
}
```

The `Port` interface is deliberately minimal — `io.ReadWriteCloser` plus `Flush()` for pre-reset buffer draining. `Drain()` (wait for output transmission) is omitted because ASH does not require it.

```go
// ash/ash.go

// Conn manages an ASH connection over a serial port.
type Conn struct {
    port   serial.Port
    frames chan []byte // received frames from reader goroutine
    wg     sync.WaitGroup // tracks reader goroutine lifecycle
    cancel context.CancelFunc
    // sequence tracking
    frmNum uint8 // next outgoing frame number (0-7)
    ackNum uint8 // next expected incoming frame number (0-7)
}

// Send transmits an EZSP payload over ASH and returns the response payload.
func (c *Conn) Send(ctx context.Context, data []byte) ([]byte, error)

// Reset performs the ASH RST/RSTACK handshake.
func (c *Conn) Reset(ctx context.Context) error

// Close shuts down the connection and waits for the reader goroutine to exit.
func (c *Conn) Close() error
```

### Implementation Phases

#### Phase 1: Scaffolding and Serial Port

Establish the project foundation and prove we can open and configure a serial port.

**Tasks:**

- [x] Initialize Go module: `go mod init github.com/esnunes/zigboo`
- [x] Add `golang.org/x/sys` dependency
- [x] Create `serial/port.go` — `Port` interface, `Config` struct with `setDefaults()`
- [x] Create `serial/unix.go` — termios implementation behind `//go:build unix` tag
- [x] Create `serial/serial_test.go` — mock port tests, config defaults
- [x] Create `cmd/zigboo/main.go` — CLI skeleton with `--port` flag, `log/slog`, signal handling

**Serial port termios configuration:**

```
Raw mode (cfmakeraw equivalent)
Baud rate: 115200 (B115200)
Character size: CS8
Stop bits: 1 (CSTOPB clear)
Parity: none (PARENB clear)
Flow control: none (CRTSCTS off, IXON/IXOFF off — ASH handles flow control bytes via byte stuffing)
CLOCAL: set (ignore modem control — prevents open() blocking on carrier detect)
HUPCL: clear (don't hang up on close)
VMIN: 1 (block until at least 1 byte)
VTIME: 1 (100ms inter-byte timeout — enables periodic cancellation checks)
```

On macOS, users should use `/dev/cu.usbserial-*` (not `/dev/tty.*`) to avoid carrier-detect blocking.

**Success criteria:** Can open the serial port, configure termios, read/write bytes, and close cleanly.

**Research Insights:**

- Open with `syscall.O_RDWR | syscall.O_NOCTTY | syscall.O_CLOEXEC` — `O_NOCTTY` prevents the serial port from becoming the controlling terminal; `O_CLOEXEC` prevents fd leaks to child processes
- Use `testing/iotest.OneByteReader` to wrap mock ports in tests — this exposes edge cases where `Read()` returns fewer bytes than requested
- The fd returned by `Open()` is owned by the `Port` — document that callers must not close it directly. `Close()` should be idempotent (use `sync.Once`)
- Define a `mockPort` test helper struct implementing `Port` with `io.ReadWriter` + `Flush()` — reuse across `ash` package tests via `internal/testutil` or by passing the interface
- The `go.bug.st/serial` library solves the blocked-Read-on-Close problem (Go issue #10001) via a self-pipe trick — worth studying their approach, though our VTIME=1 solution is simpler and sufficient for this milestone

**Files:**

- `go.mod`
- `serial/port.go`
- `serial/unix.go`
- `serial/serial_test.go`
- `cmd/zigboo/main.go`

#### Phase 2: ASH Reset Handshake

Prove bidirectional communication with the dongle via the ASH reset protocol.

**Tasks:**

- [x] Create `ash/frame.go` — frame type constants, CRC-CCITT, byte stuffing/unstuffing, data randomization (seed=0x42, polynomial=0xB8)
- [x] Create `ash/frame_test.go` — test CRC, byte stuffing, randomization with known test vectors
- [x] Create `ash/ash.go` — `Conn` type with `New(port serial.Port) (*Conn, error)`, `Reset() error` method
- [x] Implement reader goroutine with cancellation via VTIME + done channel
- [x] Implement pre-reset buffer drain (call `port.Flush()` before sending RST) — flush must happen before reader goroutine starts
- [x] Send 32 CANCEL bytes (0x1A) before RST frame (bellows convention — forces NCP to discard any partial frame in its buffer)
- [x] Implement RST frame send → RSTACK frame receive with timeout
- [x] Handle ERROR frame (log error code, return error)
- [x] Create `ash/ash_test.go` — test reset handshake with mock port
- [x] Wire CLI `reset` subcommand: `zigboo --port /dev/ttyUSB0 reset`

**ASH frame encoding details:**

```
RST frame:     [0xC0] [CRC-hi] [CRC-lo] [0x7E]
RSTACK frame:  [0xC1] [version] [reset-code] [CRC-hi] [CRC-lo] [0x7E]
ERROR frame:   [0xC2] [version] [reset-code] [error-code] [CRC-hi] [CRC-lo] [0x7E]

Reserved bytes requiring escape (0x7D XOR 0x20):
  0x7E (flag), 0x7D (escape), 0x11 (XON), 0x13 (XOFF), 0x18 (cancel), 0x1A (substitute)
```

**Reader goroutine design:**

```go
// Reader goroutine lifecycle:
// - Started by Conn.New()
// - Reads bytes from port, assembles frames delimited by 0x7E
// - Sends complete frames on a buffered channel (cap: 8)
// - Checks done channel between reads (VTIME=100ms provides regular wake-ups)
// - Exits when done channel is closed or port returns an error
// - Signals termination by closing the frames channel
```

**Maximum frame buffer:** 512 bytes. If exceeded without a 0x7E delimiter, discard buffer and resynchronize on the next 0x7E.

**RST retry strategy:** Send RST, wait up to 5 seconds for RSTACK. If no RSTACK, retry up to 3 times. If all retries fail, return error with diagnostic message suggesting baud rate or device path issues.

**Success criteria:** `zigboo --port /dev/ttyUSB0 reset` sends RST, receives and prints RSTACK (version + reset code).

**Research Insights:**

- CRC-CCITT: init=0xFFFF, polynomial=0x1021, big-endian output. CRC covers the control byte + data field (everything between the preceding flag and the CRC bytes themselves). Use a 256-entry lookup table for performance.
- Frame encoding order: assemble (control + data) → compute CRC → append CRC big-endian → randomize bytes after control byte → byte-stuff entire frame → append 0x7E flag
- Frame decoding order: strip 0x7E flag → unstuff bytes → de-randomize bytes after control byte → verify CRC → extract control byte + data
- 512-byte limit applies to the on-wire (pre-unstuff) frame. After unstuffing, the maximum payload is smaller.
- Use `select` with done channel when sending on the frames channel to prevent goroutine hangs during shutdown
- Validate frame type bytes after CRC verification — reject frames with unknown control byte patterns
- Define sentinel errors: `var ErrTimeout = errors.New("ash: timeout")`, `var ErrConnectionReset = errors.New("ash: connection reset")`

**Test vectors (from bellows):**

```
RST frame (on wire, after stuffing):
  1A C0 38 BC 7E
  (0x1A cancel prefix not part of frame — some implementations prepend it)
  Frame bytes: [0xC0] [CRC=0x38BC] [0x7E]

RSTACK frame (version 11, reset code 0x01=power-on):
  C1 02 0B 0A 52 7E
  (unstuffed: [0xC1] [0x02] [0x0B] [CRC] [0x7E])
```

**Files:**

- `ash/frame.go`
- `ash/frame_test.go`
- `ash/ash.go`
- `ash/ash_test.go`

#### Phase 3: ASH DATA Frame Exchange

Enable reliable bidirectional data transfer over ASH, required for EZSP commands.

**Tasks:**

- [x] Implement DATA frame encoding: `[control-byte] [data...] [CRC-hi] [CRC-lo] [0x7E]`
- [x] Implement data randomization (XOR with LFSR, seed=0x42, polynomial=0xB8) — applied to all bytes after control byte (data + CRC), after CRC computation, before byte stuffing
- [x] Implement ACK frame send/receive
- [x] Implement NAK handling (retransmit on NAK)
- [x] Implement sequence number tracking (3-bit, 0-7)
- [x] Implement `Send(ctx context.Context, data []byte) ([]byte, error)` — send DATA frame, wait for ACK + response DATA
- [x] Implement fixed retransmission timeout (1.6 seconds, per UG101 initial value)
- [x] Window size: 1 (send one DATA, wait for ACK before next)
- [x] Handle unsolicited RSTACK: clear state, return connection-reset error
- [x] Handle corrupt frames (CRC mismatch): discard and log
- [x] Test DATA/ACK exchange with mock port using known-good frame bytes

**DATA frame control byte:**
```
Bits 7-4: 0 (marks this as DATA frame)
Bits 2-0: frmNum (sender's frame number, 0-7)
Bit 3:    reTx (1 = retransmission)
Bits 6-4: ackNum (acknowledges peer's frames up to ackNum-1)
```

**ACK frame control byte:**
```
Bits 7-5: 100 (marks this as ACK)
Bits 2-0: ackNum (acknowledges peer's frames up to ackNum-1)
Bit 3:    nRdy (1 = not ready for more DATA)
```

- [x] Implement 5-consecutive-failure rule: if 5 retransmissions fail without ACK, declare connection lost and return error

**Success criteria:** Can send arbitrary byte payloads through ASH and receive responses reliably.

**Research Insights:**

- LFSR algorithm: shift right, XOR with polynomial if LSB was 1. First 10 bytes of the pseudo-random sequence: `0x42 0x21 0xA8 0x54 0x2A 0x15 0xB2 0x59 0x94 0x4A`. Use these as test vectors.
- DATA frame control byte ranges: `0x00-0x7F` = DATA, `0x80-0x9F` = ACK, `0xA0-0xBF` = NAK, `0xC0` = RST, `0xC1` = RSTACK, `0xC2` = ERROR. Use named constants for all of these.
- Randomization applies to everything after the control byte (data + CRC bytes) — NOT the control byte itself. The LFSR resets to seed for each frame.
- Use `sync.WaitGroup` to track the reader goroutine — `Close()` should `wg.Wait()` after signaling done to ensure clean shutdown
- Read buffer size: 256 bytes per `Read()` call is a good balance between syscall overhead and responsiveness
- Frame buffer reuse: pre-allocate a `[512]byte` array, copy completed frames out. Avoids per-frame allocation.
- In-place byte unstuffing on the decode path avoids a second buffer allocation

**Test vector (DATA frame carrying EZSP version command):**

```
EZSP version(4) payload: [0x00 0x00 0x00 0x04]
  (seq=0, fc=0x00, frameID=0x00, desiredVersion=4)
DATA frame (frmNum=0, ackNum=0):
  Control byte: 0x00
  Data (before randomize): 0x00 0x00 0x00 0x04
  After randomize (XOR with 0x42 0x21 0xA8 0x54): 0x42 0x21 0xA8 0x50
  CRC computed over: [0x00] [0x00 0x00 0x00 0x04] = (verify against implementation)
```

**Files:**

- `ash/frame.go` (extended)
- `ash/frame_test.go` (extended)
- `ash/ash.go` (extended)
- `ash/ash_test.go` (extended)

#### Phase 4: EZSP Version Negotiation

Prove end-to-end protocol stack by negotiating EZSP version with the NCP.

**Tasks:**

- [x] Create `ezsp/frame.go` — EZSP frame encoding/decoding for both formats:
  - Legacy (v4-v8): `[seq(1)] [fc(1)] [frameID(1)] [params...]`
  - Extended (v9+): `[seq(1)] [fc(2)] [frameID(2)] [params...]`
- [x] Create `ezsp/commands.go` — command ID constants (version=0x00, etc.)
- [x] Create `ezsp/ezsp.go` — `Client` type with `New(conn *ash.Conn) *Client`
- [x] Implement two-phase version negotiation:
  1. Send `version(4)` in legacy format
  2. NCP responds with `(protocolVersion, stackType, stackVersion)`
  3. If protocolVersion >= 9: re-send `version(protocolVersion)` in extended format
  4. NCP confirms with extended-format response
- [x] Implement EZSP sequence number management (uint8, monotonic, wrapping)
- [x] Implement command/response correlation by sequence number
- [x] Discard unexpected frames that don't match the pending command's sequence number (log at debug level)
- [x] Create `ezsp/frame_test.go` — test frame encoding with known test vectors
- [x] Create `ezsp/ezsp_test.go` — test version negotiation with mock ASH conn
- [x] Wire CLI `version` subcommand: `zigboo --port /dev/ttyUSB0 version`

**EZSP frame control bits (extended format, v9+):**
```
Byte 0, Bit 7: Overflow (NCP ran out of memory since last response)
Byte 0, Bit 6: Truncated (response was truncated)
Byte 0, Bit 5: Callback pending (more callbacks to send)
Byte 0, Bit 4: Callback (this frame IS a callback, not a command response)
Byte 0, Bit 3: Security enabled
Byte 0, Bits 2-0: Sleep mode
Byte 1: Reserved (0x00)
```

**Success criteria:** `zigboo --port /dev/ttyUSB0 version` prints the negotiated EZSP protocol version, stack type, and stack version.

**Research Insights:**

- EZSP legacy format: 3-byte header `[seq(1)] [fc(1)] [frameID(1)]`. Extended format (v9+): 5-byte header `[seq(1)] [fc_lo(1)] [fc_hi(1)] [frameID_lo(1)] [frameID_hi(1)]`. The `fc_hi` byte is always `0x01` in extended format (identifies it as extended). Frame IDs are little-endian uint16.
- The first `version` command MUST use legacy format regardless of the NCP's actual version — this is how the NCP knows you're starting fresh
- Add an EZSP command timeout (5 seconds default) — if no response arrives within this window, return an error. This catches cases where ASH ACKs the frame but the NCP never sends an EZSP response.
- `version` response parameters: `protocolVersion(1)`, `stackType(1)`, `stackVersion(2 LE)`. Stack version encodes as `major.minor.patch` in the uint16 (varies by firmware).
- Callback detection and EZSP overflow bit handling are deferred to a future milestone — for now, simply discard any frame whose sequence number doesn't match the pending command

**Files:**

- `ezsp/frame.go`
- `ezsp/frame_test.go`
- `ezsp/commands.go`
- `ezsp/ezsp.go`
- `ezsp/ezsp_test.go`

#### Phase 5: Basic EZSP Commands and CLI Polish

Make the tool useful by adding queries and polishing the CLI output.

**Tasks:**

- [x] Implement `getNodeId` EZSP command (frameID=0x0027)
- [x] Implement `getEui64` EZSP command (frameID=0x0026) — the dongle's IEEE address
- [x] Wire CLI subcommands: `info` (combines version + node-id + eui64)
- [x] Add `--verbose` / `-v` flag for debug logging (hex frame dumps with TX/RX direction and timestamps)
- [x] Add clear error messages for common failures:
  - Device not found: "device not found: /dev/ttyUSB0 — check the path and ensure the dongle is plugged in"
  - Permission denied: "permission denied: /dev/ttyUSB0 — add your user to the 'dialout' group (Linux) or check System Settings > Privacy (macOS)"
  - Timeout on reset: "dongle not responding — check baud rate (expected 115200) and ensure no other application is using the port"
- [x] Add graceful shutdown: context cancellation on SIGINT, close serial port, wait for reader goroutine

**CLI interface:**

```
zigboo --port /dev/ttyUSB0 reset           # ASH reset, print RSTACK info
zigboo --port /dev/ttyUSB0 version         # EZSP version negotiation
zigboo --port /dev/ttyUSB0 info            # version + node-id + eui64
zigboo -v --port /dev/ttyUSB0 info         # verbose mode with frame dumps
```

Output format: human-readable text. JSON output deferred to a future milestone.

**Success criteria:** `zigboo --port /dev/ttyUSB0 info` prints dongle firmware version, node ID, and EUI-64 address.

**Research Insights:**

- `getNodeId` returns a 2-byte little-endian uint16 (the short network address, 0xFFFE when not joined)
- `getEui64` returns an 8-byte little-endian IEEE 802.15.4 address — display as colon-separated hex (e.g., `00:12:4B:00:25:89:73:14`)
- `getNetworkParameters` is deferred to a future milestone — it requires parsing the `EmberNetworkParameters` struct which adds complexity without proving more of the protocol stack
- Use `sync.WaitGroup` in the CLI's shutdown path: cancel context → wait for EZSP client to drain → wait for ASH conn to close → wait for reader goroutine to exit → close serial port

**Files:**

- `ezsp/commands.go` (extended)
- `ezsp/ezsp.go` (extended)
- `cmd/zigboo/main.go` (extended)

## Explicitly Out of Scope (Milestone 1)

- Zigbee network formation, device pairing, ZCL
- MQTT bridge
- Adaptive retransmission timeouts (fixed at 1.6s)
- ASH sliding window > 1
- Callback processing (beyond discarding non-matching sequence numbers)
- EZSP overflow/truncated bit handling
- `getNetworkParameters` EZSP command
- Secure EZSP (AN1125)
- Serial port auto-detection
- JSON CLI output
- Windows support
- Reconnection/recovery loops (exit on fatal error)
- Web UI

## Acceptance Criteria

### Functional Requirements

- [x] `zigboo --port <path> reset` performs ASH RST/RSTACK handshake and prints result
- [x] `zigboo --port <path> version` negotiates EZSP version (including v9+ two-phase) and prints result
- [x] `zigboo --port <path> info` prints EZSP version, node ID, and EUI-64
- [ ] Works on both macOS and Linux with the Sonoff ZBDongle-E

### Non-Functional Requirements

- [x] All packages have unit tests using stdlib `testing` (no third-party test libraries)
- [x] Port interface enables testing without hardware (mock port in tests)
- [x] `--verbose` flag shows hex frame dumps for protocol debugging
- [x] Clear, actionable error messages for device-not-found, permission-denied, and timeout scenarios
- [x] Graceful shutdown on SIGINT (no goroutine leaks)
- [x] Maximum frame buffer capped at 512 bytes (no unbounded allocation)

### Quality Gates

- [x] `go test ./...` passes
- [x] `go vet ./...` clean
- [x] Every exported symbol has a doc comment
- [x] Every package has a package doc comment

## Dependencies and Prerequisites

- **Hardware:** Sonoff ZBDongle-E with NCP firmware (EmberZNet, any version)
- **Go toolchain:** Go 1.25.5 (managed via mise)
- **Dependencies:** `golang.org/x/sys` (for unix termios syscalls)
- **Reference specs:** UG101 (ASH), UG100/UG600 (EZSP)
- **Reference implementations:** bellows (Python), zigbee-herdsman ember adapter (TypeScript)

## Risk Analysis and Mitigation

| Risk | Impact | Mitigation |
|------|--------|------------|
| Reader goroutine hangs on Close() (Go issue #10001) | Program hangs on shutdown | Use VTIME=1 (100ms) for periodic wake-up + context cancellation; `sync.WaitGroup` in `Close()` |
| Wrong termios settings cause corrupt/missing data | Can't communicate with dongle | Start with known-good config from bellows; test with real hardware early |
| EZSP v9+ re-negotiation fails | Can't talk to modern firmware | Implement full two-phase handshake per UG100; test with real dongle |
| macOS tty.* device blocks on open() | macOS users can't connect | Document cu.* usage; set CLOCAL in termios; open with O_NOCTTY |
| Data randomization LFSR produces wrong sequence | Every DATA frame corrupt | Use known test vectors: first 10 LFSR bytes are `0x42 0x21 0xA8 0x54 0x2A 0x15 0xB2 0x59 0x94 0x4A` |
| fd leak to child processes | Security concern | Open with O_CLOEXEC flag |
| NCP unresponsive after partial frame | Stuck connection | Send 32 CANCEL bytes (0x1A) before RST; 5-consecutive-failure rule triggers connection-lost |

## Conventions to Follow

Per the user's established Go patterns (from osqueue):

- `New()` constructors returning `(*Type, error)`
- `Config` structs with private `setDefaults()` methods
- Sentinel errors: `var ErrTimeout = errors.New("ash: timeout")`, `var ErrConnectionReset = errors.New("ash: connection reset")`
- Error wrapping: `fmt.Errorf("ash: reset: %w", err)`
- `context.Context` as first parameter on all blocking operations
- `log/slog` for logging
- `flag` package for CLI (no cobra)
- Stdlib-only testing with `t.Helper()` helpers
- `t.Cleanup()` for test teardown
- Table-driven tests with `t.Run()` subtests for frame encoding/decoding
- Named constants for all protocol magic numbers (frame types, reserved bytes, timeouts)
- Package doc comments on every package

## References

### Internal

- Brainstorm: `docs/brainstorms/2026-03-01-zigbee-serial-communication-brainstorm.md`

### External

- [UG101: UART-EZSP Gateway Protocol Reference (ASH)](https://www.silabs.com/documents/public/user-guides/ug101-uart-gateway-protocol-reference.pdf)
- [UG100: EZSP Reference Guide](https://www.silabs.com/documents/public/user-guides/ug100-ezsp-reference-guide.pdf)
- [UG600: Simplicity SDK EZSP Reference Guide](https://www.silabs.com/documents/public/user-guides/ug600-ezsp-reference-guide.pdf)
- [bellows (Python EZSP)](https://github.com/zigpy/bellows)
- [zigbee-herdsman (TypeScript, ember adapter)](https://github.com/Koenkk/zigbee-herdsman)
- [Go issue #10001: Race between Close() and Read()](https://github.com/golang/go/issues/10001)
