# zigboo

A Zigbee coordinator written in Go. Communicates with a Sonoff ZBDongle-E (Silicon Labs EFR32MG21) via serial UART using the EZSP/ASH protocol stack.

## Installation

```sh
go install github.com/esnunes/zigboo/cmd/zigboo@latest
```

## Usage

```sh
zigboo --port /dev/cu.usbserial-1420 reset              # ASH reset handshake
zigboo --port /dev/cu.usbserial-1420 version            # EZSP version negotiation
zigboo --port /dev/cu.usbserial-1420 info               # version + node-id + eui64
zigboo --port /dev/cu.usbserial-1420 network            # network state and parameters
zigboo --port /dev/cu.usbserial-1420 scan               # energy channel scan (default)
zigboo --port /dev/cu.usbserial-1420 scan --type active # active channel scan
zigboo --port /dev/cu.usbserial-1420 endpoints          # list registered endpoints and clusters
zigboo --port /dev/cu.usbserial-1420 config list        # dump all NCP config values
zigboo --port /dev/cu.usbserial-1420 config get <name>  # read one config value
zigboo --port /dev/cu.usbserial-1420 config set <name> <value> # write one config value
zigboo -v --port /dev/cu.usbserial-1420 info            # verbose mode with frame dumps
```

### Device Path

On **macOS**, use `/dev/cu.usbserial-*` (not `/dev/tty.*`) to avoid blocking on carrier detect:

```sh
ls /dev/cu.usbserial-*
```

On **Linux**, the device is typically `/dev/ttyUSB0`. You may need to add your user to the `dialout` group:

```sh
sudo usermod -aG dialout $USER
```

## Packages

| Package | Description |
|---------|-------------|
| `serial` | Serial port abstraction with unix termios implementation |
| `ash` | ASH transport layer (framing, CRC, byte stuffing, retransmission) |
| `ezsp` | EZSP command layer (version negotiation, device queries) |
| `cmd/zigboo` | CLI entry point |

## Hardware

Tested with the Sonoff ZBDongle-E (EFR32MG21, WCH CH9102F USB bridge) running EmberZNet NCP firmware at 115200 baud.
