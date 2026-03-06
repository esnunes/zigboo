# Brainstorm: Support Devices Joining the Network

**Date:** 2026-03-05
**Status:** Complete
**Feature:** Full device pairing flow -- detect join events, interview devices, persist device info

## What We're Building

An end-to-end device pairing flow for zigboo. When a user runs `zigboo pair`, the host:

1. Opens the network for joining (permit-join)
2. Listens for join events via trust center callbacks
3. Interviews the newly joined device (ZDO descriptors + ZCL Basic attributes)
4. Persists device information to a JSON file
5. Streams progress to the terminal in real-time
6. Exits on timeout or Ctrl+C

This transforms zigboo from "can form a network" to "can onboard devices."

## Why This Approach

| Decision | Choice | Rationale |
|---|---|---|
| Architecture | Layered services -- new `host` package on top of `ezsp/` | The NCP is the coordinator; zigboo is the host. `host/` owns session lifecycle, reader goroutine, callback dispatch, and device state. `ezsp/` stays stateless (command encoding/decoding only). |
| Callback model | Single reader goroutine in `host/` with response channel + callback dispatch | `host/` owns the reader goroutine. Reads all ASH frames, classifies them as command responses (routed to `ezsp.Client` via channel) or callbacks (dispatched to handlers). `ezsp/` stays stateless — `Command()` sends via ASH, waits on a response channel. |
| APS messaging | Build as a separate prerequisite milestone | Large enough to warrant its own plan. Device joining depends on sendUnicast + incomingMessageHandler for ZDO/ZCL. |
| Trust center policy | Well-known HA link key (open join) | Standard default, simplest starting point. Install-code provisioning planned as a future enhancement. |
| Device persistence | JSON file on disk | Simple, inspectable, no dependencies. Sufficient for the expected device count (~100s). |
| Device interview | Node descriptor + active endpoints + simple descriptors + ZCL Basic attributes | Gives complete device identity: what it is (descriptor), what it does (clusters), and who made it (manufacturer, model, firmware). |
| CLI experience | Interactive `zigboo pair` command | Opens permit-join, streams events, runs interview automatically. Best UX for the primary use case. |
| Package structure | Separate `zdo/` and `zcl/` packages | Protocol layers with their own framing, likely reused beyond `host/`. |
| Storage design | JSON file | Simple and inspectable. Add a storage interface when daemon mode or an alternative backing store is actually needed. |
| Device filtering | No filtering (YAGNI) | Accept all devices. No clear use case for filtering yet. |

## Key Decisions

### Host Package

A new `host/` package sits between `ezsp/` and `cmd/zigboo/`. The NCP is the Zigbee coordinator; zigboo is the host.

- **Owns the reader goroutine** -- continuously reads from ASH, classifies frames as command responses or callbacks
- **Routes responses to `ezsp.Client`** -- via a channel, so `Command()` sends a request and waits for its response without reading from ASH directly
- **Dispatches callbacks** -- to registered handler functions (e.g., trust center join, child join)
- **Manages device state** -- in-memory device table + JSON persistence
- **Exposes high-level operations** -- `Pair(ctx, duration)`, `Devices()`, etc.
- **`ezsp/` stays stateless** -- command encoding/decoding only, no goroutines or session management

### Callback Infrastructure

The current `Command()` loop skips callbacks inline. The new model:

- `host/` reader goroutine reads all frames from the ASH connection
- Command responses are routed to `ezsp.Client` via a response channel
- Callbacks are dispatched to registered handler functions
- Key callbacks: `trustCenterJoinHandler` (0x0024), `childJoinHandler` (0x0023)
- `ezsp.Client.Command()` changes: sends via ASH, then blocks on response channel instead of calling `Recv()` directly

### Device Interview Sequence

After a device joins:

1. **Node Descriptor Request** (ZDO) -- device type, manufacturer code, capabilities
2. **Active Endpoints Request** (ZDO) -- list of active endpoint numbers
3. **Simple Descriptor Request** (ZDO) -- per-endpoint: profile, device ID, in/out clusters
4. **Read Basic Cluster Attributes** (ZCL) -- manufacturer name, model identifier, firmware version

### Pairing CLI Flow

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
      In:  OccupancySensing, Basic, PowerConfiguration
      Out: Identify

Device saved to devices.json

Waiting for more devices... (98s remaining)
^C
Network closed for joining.
```

### Pre-conditions

The `zigboo pair` command requires an active network. It should check network state on startup and error with a clear message if no network is formed.

## Prerequisites

These must be built before the pairing flow:

1. **Host package + callback infrastructure** -- reader goroutine in `host/`, response routing to `ezsp.Client`, callback dispatch
2. **APS messaging** -- `sendUnicast`, `incomingMessageHandler` (new EZSP commands in `ezsp/`)
3. **ZDO commands** -- node descriptor, active endpoints, simple descriptor requests (in `zdo/`)
4. **ZCL frame construction** -- at minimum, Read Attributes for Basic cluster (in `zcl/`)

## Resolved Questions

1. **Should ZDO and ZCL be separate packages or part of host?** Separate packages (`zdo/` and `zcl/`). They are protocol layers with their own framing and will likely be used by other consumers beyond `host/`.

2. **How should the device JSON file handle concurrent access?** Daemon mode is planned but not in scope. Start with simple JSON file. Add a storage interface when daemon mode is actually built.

3. **Should the pair command support filtering by device type?** No -- YAGNI. Accept all devices that join.
