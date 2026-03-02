// Package ezsp implements the EZSP (EmberZNet Serial Protocol) command layer
// for communicating with Silicon Labs Zigbee network co-processors.
//
// EZSP sits on top of the ASH transport layer and provides command/response
// semantics for controlling the Zigbee stack on the NCP. This package handles
// frame encoding/decoding, version negotiation, and command dispatch.
// See UG100/UG600 for the full specification.
package ezsp

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/esnunes/zigboo/ash"
)

// Sentinel errors.
var (
	// ErrFrameTooShort indicates an EZSP frame is shorter than the minimum header size.
	ErrFrameTooShort = errors.New("ezsp: frame too short")

	// ErrUnexpectedResponse indicates the response frame ID didn't match the request.
	ErrUnexpectedResponse = errors.New("ezsp: unexpected response")

	// ErrCommandTimeout indicates no EZSP response was received within the timeout.
	ErrCommandTimeout = errors.New("ezsp: command timeout")
)

const (
	// commandTimeout is the maximum time to wait for an EZSP response.
	commandTimeout = 5 * time.Second
)

// VersionInfo holds the result of EZSP version negotiation.
type VersionInfo struct {
	ProtocolVersion byte
	StackType       byte
	StackVersion    uint16
}

// StackVersionString formats the stack version as "major.minor.patch".
func (v VersionInfo) StackVersionString() string {
	major := v.StackVersion >> 12
	minor := (v.StackVersion >> 8) & 0x0F
	patch := v.StackVersion & 0xFF
	return fmt.Sprintf("%d.%d.%d", major, minor, patch)
}

// Client is an EZSP command client.
type Client struct {
	conn     *ash.Conn
	seq      byte // monotonically increasing sequence number
	extended bool // true if using extended frame format (v9+)
	version  byte // negotiated protocol version
}

// New creates a new EZSP client over the given ASH connection.
func New(conn *ash.Conn) *Client {
	return &Client{conn: conn}
}

// NegotiateVersion performs the two-phase EZSP version negotiation.
//
// Phase 1: Send version(4) in legacy format. The NCP responds with its
// highest supported protocol version.
// Phase 2: If the NCP supports v9+, re-send version(protocolVersion)
// in extended format. The NCP confirms with an extended-format response.
func (c *Client) NegotiateVersion(ctx context.Context) (VersionInfo, error) {
	// Phase 1: legacy format, desired version = 4.
	slog.Debug("ezsp: negotiating version (phase 1, legacy format)")
	frame := encodeLegacy(c.nextSeq(), frameIDVersion, []byte{4})

	resp, err := c.sendRaw(ctx, frame)
	if err != nil {
		return VersionInfo{}, fmt.Errorf("ezsp: version negotiation phase 1: %w", err)
	}

	info, err := parseVersionResponse(resp, false)
	if err != nil {
		return VersionInfo{}, fmt.Errorf("ezsp: version negotiation phase 1: %w", err)
	}

	slog.Debug("ezsp: phase 1 response",
		"protocolVersion", info.ProtocolVersion,
		"stackType", info.StackType,
		"stackVersion", info.StackVersionString())

	if info.ProtocolVersion < legacyVersionThreshold {
		// Legacy NCP — use legacy format going forward.
		c.version = info.ProtocolVersion
		return info, nil
	}

	// Phase 2: extended format, desired version = NCP's reported version.
	slog.Debug("ezsp: negotiating version (phase 2, extended format)",
		"desiredVersion", info.ProtocolVersion)

	c.extended = true
	frame = encodeExtended(c.nextSeq(), frameIDVersion, []byte{info.ProtocolVersion})

	resp, err = c.sendRaw(ctx, frame)
	if err != nil {
		return VersionInfo{}, fmt.Errorf("ezsp: version negotiation phase 2: %w", err)
	}

	info, err = parseVersionResponse(resp, true)
	if err != nil {
		return VersionInfo{}, fmt.Errorf("ezsp: version negotiation phase 2: %w", err)
	}

	c.version = info.ProtocolVersion

	slog.Debug("ezsp: version negotiation complete",
		"protocolVersion", info.ProtocolVersion,
		"stackType", info.StackType,
		"stackVersion", info.StackVersionString())

	return info, nil
}

// Command sends an EZSP command and returns the response parameters.
// The frame is automatically encoded in the correct format (legacy or extended).
func (c *Client) Command(ctx context.Context, frameID uint16, params []byte) ([]byte, error) {
	var frame []byte
	if c.extended {
		frame = encodeExtended(c.nextSeq(), frameID, params)
	} else {
		frame = encodeLegacy(c.nextSeq(), frameID, params)
	}

	resp, err := c.sendRaw(ctx, frame)
	if err != nil {
		return nil, err
	}

	// Decode response and extract parameters.
	var respParams []byte
	if c.extended {
		_, _, respParams, err = decodeExtended(resp)
	} else {
		_, _, respParams, err = decodeLegacy(resp)
	}
	if err != nil {
		return nil, fmt.Errorf("ezsp: decode response: %w", err)
	}

	return respParams, nil
}

// GetNodeID returns the dongle's short network address.
// Returns 0xFFFE when not joined to a network.
func (c *Client) GetNodeID(ctx context.Context) (uint16, error) {
	resp, err := c.Command(ctx, frameIDGetNodeID, nil)
	if err != nil {
		return 0, fmt.Errorf("ezsp: getNodeId: %w", err)
	}
	if len(resp) < 2 {
		return 0, fmt.Errorf("ezsp: getNodeId: response too short (%d bytes)", len(resp))
	}
	return binary.LittleEndian.Uint16(resp[:2]), nil
}

// GetEUI64 returns the dongle's IEEE 802.15.4 address (8 bytes, little-endian).
func (c *Client) GetEUI64(ctx context.Context) ([8]byte, error) {
	resp, err := c.Command(ctx, frameIDGetEUI64, nil)
	if err != nil {
		return [8]byte{}, fmt.Errorf("ezsp: getEui64: %w", err)
	}
	if len(resp) < 8 {
		return [8]byte{}, fmt.Errorf("ezsp: getEui64: response too short (%d bytes)", len(resp))
	}
	var eui [8]byte
	copy(eui[:], resp[:8])
	return eui, nil
}

// FormatEUI64 formats an EUI-64 address as colon-separated hex.
func FormatEUI64(eui [8]byte) string {
	return fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X:%02X:%02X",
		eui[7], eui[6], eui[5], eui[4], eui[3], eui[2], eui[1], eui[0])
}

// sendRaw sends raw EZSP frame bytes over ASH and returns the raw response.
func (c *Client) sendRaw(ctx context.Context, frame []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	resp, err := c.conn.Send(ctx, frame)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// nextSeq returns the next EZSP sequence number (uint8, monotonic, wrapping).
func (c *Client) nextSeq() byte {
	seq := c.seq
	c.seq++
	return seq
}

// parseVersionResponse parses the parameters from a version command response.
func parseVersionResponse(data []byte, extended bool) (VersionInfo, error) {
	var params []byte
	var err error

	if extended {
		_, _, params, err = decodeExtended(data)
	} else {
		_, _, params, err = decodeLegacy(data)
	}
	if err != nil {
		return VersionInfo{}, err
	}

	if len(params) < 4 {
		return VersionInfo{}, fmt.Errorf("ezsp: version response too short (%d bytes)", len(params))
	}

	return VersionInfo{
		ProtocolVersion: params[0],
		StackType:       params[1],
		StackVersion:    binary.LittleEndian.Uint16(params[2:4]),
	}, nil
}
