# Network Commands Brainstorm

**Date:** 2026-03-03
**Status:** Draft

## What We're Building

A set of EZSP commands and CLI subcommands for managing Zigbee network lifecycle: initialization, formation, state inspection, and device joining.

### EZSP Commands (ezsp package)

1. **networkInit** (`0x0017`) - Attempts to resume a previously formed network from NCP stored state. Returns EmberStatus indicating success (joined) or failure (no network).
2. **formNetwork** (`0x001E`) - Forms a new Zigbee network with given parameters (channel, PAN ID, TX power, extended PAN ID). Requires security state to be set first.
3. **setInitialSecurityState** (`0x0068`) - Configures the security key material before forming a network. Will use the well-known Zigbee HA (Home Automation) link key automatically.
4. **permitJoining** (`0x0022`) - Opens or closes the network for device joining with a specified duration.

### CLI Commands (network subcommand group)

The existing `network` top-level command becomes a subcommand group:

```
zigboo network init [--channel N] [--pan-id N] [--tx-power N]
zigboo network state
zigboo network params
zigboo network permit-join [--duration N] [--broadcast]
```

## Why This Approach

### Subcommand group over flat commands

Grouping under `network` mirrors the logical relationship between these operations and follows the existing `scan` and `config` subcommand patterns. Renaming the current `network` command to `network params` is a minor breaking change but improves consistency.

### HA defaults for security

Using the well-known Zigbee HA link key (`ZigBeeAlliance09`) avoids requiring users to manage key material for standard home automation setups. This matches what most Zigbee coordinators (zigbee2mqtt, ZHA) do by default.

### network init as an orchestrator

The `network init` CLI command provides a single entry point:
1. Call `setInitialSecurityState` with HA defaults
2. Call `networkInit` to attempt resuming stored network
3. If networkInit fails (no stored network), call `formNetwork` with provided params
4. Call `networkState` and `getNetworkParameters` to display final state

This gives users one command to "get the network running" regardless of whether it's a fresh start or a resume.

### permitJoining as separate command

Joining is a distinct operational concern (security-sensitive, time-limited) that should always be an explicit user action, never automatic.

## Key Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| CLI organization | `network` subcommand group | Groups related commands, follows `scan`/`config` pattern |
| formNetwork params | Flags with defaults | Flexibility for advanced users, simplicity for common case |
| Security setup | Auto-set HA link key | Standard for home automation, matches zigbee2mqtt/ZHA behavior |
| permitJoining scope | Duration + broadcast flags | Full control over join window and network-wide propagation |
| Auto permit-join on form | No | Joining is security-sensitive; always explicit |
| network init behavior | orchestrator (init -> form fallback -> show state) | Single command for "make network ready" |

## Scope Details

### formNetwork defaults

- `--channel`: If omitted, use channel 11 (common default for Zigbee HA)
- `--pan-id`: If omitted, use 0xFFFF (let NCP auto-select)
- `--tx-power`: If omitted, use 8 (reasonable default, units: dBm)

### permitJoining defaults

- `--duration`: Default 60 seconds. Value 0 closes joining. Value 255 opens indefinitely.
- `--broadcast`: If set, broadcast permit-join to entire network. Default: local coordinator only.

### setInitialSecurityState details

The well-known HA link key is: `5A 69 67 42 65 65 41 6C 6C 69 61 6E 63 65 30 39` ("ZigBeeAlliance09").

Security bitmask flags to set:
- `EMBER_HAVE_PRECONFIGURED_KEY` (0x0100)
- `EMBER_HAVE_NETWORK_KEY` (0x0200) - generate random network key
- `EMBER_REQUIRE_ENCRYPTED_KEY` (0x0800) - for Install Code security

### EmberNetworkInitBitmask

networkInit uses `EmberNetworkInitBitmask` parameter (EZSP v9+):
- `EMBER_NETWORK_INIT_NO_OPTIONS` (0x0000) - standard init

## Open Questions

None - all key decisions have been resolved through discussion.
