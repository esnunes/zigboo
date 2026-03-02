# Brainstorm: Zigbee Serial Communication (zigboo)

**Date:** 2026-03-01
**Status:** Complete

## What We're Building

A zigbee2mqtt replacement written in Go. The application communicates with a Sonoff ZBDongle-E (Silicon Labs EFR32MG21) via serial UART to control Zigbee home automation devices.

The project is structured as a **reusable library + CLI application**:
- Library packages (`ash`, `ezsp`, `zigbee`, etc.) that others can use
- A CLI/service binary in `cmd/` for running as a coordinator

### Long-term vision

Full-featured Zigbee coordinator with MQTT bridge — replacing zigbee2mqtt with a single Go binary that handles device pairing, state management, ZCL commands, and MQTT publishing.

### First milestone: ASH + EZSP basics

Prove we can talk to the dongle:
1. Open serial connection at 115200 baud (8N1)
2. Perform ASH reset handshake (RST → RSTACK)
3. Negotiate EZSP protocol version
4. Run basic EZSP commands (get network parameters, get node ID, etc.)
5. CLI tool to execute these operations

## Why This Approach

**Integration-First Vertical Slice** — get a minimal end-to-end connection to the dongle working first, then harden protocol handling iteratively.

Reasons:
- Faster feedback loop with real hardware surfaces protocol quirks early
- Avoids over-engineering protocol layers before understanding real-world behavior
- The dongle may have firmware-specific behaviors not documented in specs
- Motivation stays high when you see real results quickly

We'll still keep clean package boundaries (ash, ezsp) but implement just enough of each layer to get the next integration point working.

### Reference materials

- **Primary:** Silicon Labs specs — UG101 (ASH framing), UG100/UG600 (EZSP commands)
- **Secondary:** bellows Python source (most readable), zigbee-herdsman ember adapter TypeScript source (newest, targets EZSP v13+)
- Note: zigbee2mqtt migrated from the "ezsp" driver to the newer "ember" driver

## Key Decisions

1. **Language: Go** — chosen for performance, single binary deployment, strong concurrency primitives (goroutines/channels map well to async serial I/O)

2. **Structure: Library + CLI** — reusable packages under the module root, CLI binary in `cmd/zigboo/`

3. **Approach: Integration-first** — minimal viable protocol handling to talk to hardware, then iterate

4. **EZSP version support:** Start with version negotiation from v4 (per spec), support whatever the NCP reports. Firmware version unknown, so runtime detection is essential.

5. **Serial port: built from scratch (unix only)** — implement directly using `golang.org/x/sys/unix` termios syscalls (~50-100 lines). No Windows support initially. No third-party serial library dependency.

6. **Testing: interface-based mocking + recorded traces** — define a `Port` interface (`io.ReadWriteCloser` + configuration) that ASH depends on. Unit tests use mocks simulating NCP responses. Later, capture real serial traffic for integration test replay.

7. **Concurrency: goroutines + channels** — dedicated reader goroutine for the serial port, frames dispatched via channels. EZSP callbacks and command responses demultiplexed by the EZSP layer.

8. **Target platforms:** macOS for development, Linux for production. Unix-only serial implementation (no Windows).

### Protocol stack

```
CLI (cmd/zigboo)
  |
EZSP package (command encoding, version negotiation, callbacks)
  |
ASH package (framing, CRC, byte stuffing, retransmission, reset)
  |
Serial package (unix termios, Port interface)
  |
/dev/ttyUSB0 or /dev/tty.usbserial-* (115200 baud, 8N1)
  |
ZBDongle-E hardware (EFR32MG21, NCP firmware)
```

## Protocol Details (Reference)

### ASH Layer (UG101)

- Frame types: DATA, ACK, NAK, RST, RSTACK, ERROR
- Byte stuffing: reserved bytes escaped with `0x7D` XOR `0x20`
- Frame delimiter: `0x7E`
- CRC: CRC-CCITT, 16-bit, init `0xFFFF`, appended big-endian before flag byte
- Sliding window: 3-bit sequence numbers (0-7)
- Data randomization: XOR payload with pseudo-random sequence for DC balance
- Reset handshake: Host sends RST → NCP responds RSTACK
- Retransmission: adaptive timeouts (initial 1.6s, min 0.4s, max 3.2s), 5 consecutive failures = connection lost

### EZSP Layer (UG100/UG600)

- Frame format (v4-v8): `[Sequence(1)] [FrameControl(1)] [FrameID(1)] [Params...]`
- Frame format (v9+): `[Sequence(1)] [FrameControl(2)] [FrameID(2)] [Params...]`
- Little-endian byte order
- Version negotiation: start at v4, NCP responds with highest supported
- Command/response model with sequence number correlation
- Callbacks: unsolicited events from NCP (device joined, message received, etc.)

### Hardware

- **Chipset:** Silicon Labs EFR32MG21 (ARM Cortex-M33, 80 MHz)
- **USB bridge:** WCH CH9102F
- **Baud rate:** 115200 (NCP), 460800 (RCP)
- **Firmware:** EmberZNet PRO NCP (recommend 7.4.x or 8.0.x for EZSP v13+)

