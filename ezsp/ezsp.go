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
	"sync"
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

	// ErrScanInProgress is returned when a command is issued while a scan is running.
	ErrScanInProgress = errors.New("ezsp: scan in progress")
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
	mu       sync.Mutex
	scanning bool // true while a scan is in progress
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
// Returns ErrScanInProgress if a scan is currently running.
func (c *Client) Command(ctx context.Context, frameID uint16, params []byte) ([]byte, error) {
	c.mu.Lock()
	if c.scanning {
		c.mu.Unlock()
		return nil, ErrScanInProgress
	}
	c.mu.Unlock()

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

// StartEnergyScan initiates an energy scan and returns a channel of results.
//
// It blocks until the NCP confirms the scan has started. On success, it spawns
// a goroutine that reads energy scan results and sends them on the returned
// channel. The result channel (buffered, cap 16) is closed when
// scanCompleteHandler arrives. The error channel (buffered, cap 1) receives
// the scan completion status (nil on success) and is then closed.
//
// No other EZSP commands may be issued while a scan is in progress.
// Command() will return ErrScanInProgress.
func (c *Client) StartEnergyScan(ctx context.Context, channelMask uint32, duration uint8) (<-chan EnergyScanResult, <-chan error, error) {
	if err := c.startScan(ctx, ScanTypeEnergy, channelMask, duration); err != nil {
		return nil, nil, err
	}

	c.mu.Lock()
	c.scanning = true
	c.mu.Unlock()

	results := make(chan EnergyScanResult, 16)
	errCh := make(chan error, 1)

	go c.runEnergyScan(ctx, results, errCh)

	return results, errCh, nil
}

// StartActiveScan initiates an active scan and returns a channel of results.
//
// Same contract as StartEnergyScan but returns NetworkScanResult for each
// discovered network.
func (c *Client) StartActiveScan(ctx context.Context, channelMask uint32, duration uint8) (<-chan NetworkScanResult, <-chan error, error) {
	if err := c.startScan(ctx, ScanTypeActive, channelMask, duration); err != nil {
		return nil, nil, err
	}

	c.mu.Lock()
	c.scanning = true
	c.mu.Unlock()

	results := make(chan NetworkScanResult, 16)
	errCh := make(chan error, 1)

	go c.runActiveScan(ctx, results, errCh)

	return results, errCh, nil
}

// startScan sends the startScan EZSP command and validates the response.
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

// runEnergyScan reads energy scan callbacks until scanCompleteHandler.
func (c *Client) runEnergyScan(ctx context.Context, results chan<- EnergyScanResult, errCh chan<- error) {
	defer func() {
		close(results)
		close(errCh)
		c.mu.Lock()
		c.scanning = false
		c.mu.Unlock()
	}()

	for {
		raw, err := c.conn.Recv(ctx)
		if err != nil {
			errCh <- fmt.Errorf("ezsp: energyScan: recv: %w", err)
			return
		}

		frameID, params, err := c.decodeCallback(raw)
		if err != nil {
			slog.Debug("ezsp: energyScan: decode error, ignoring", "err", err)
			continue
		}

		switch frameID {
		case frameIDEnergyScanResultHandler:
			if len(params) < 2 {
				slog.Debug("ezsp: energyScan: result too short", "len", len(params))
				continue
			}
			results <- EnergyScanResult{
				Channel: params[0],
				MaxRSSI: int8(params[1]),
			}

		case frameIDScanCompleteHandler:
			if len(params) >= 2 && params[1] != 0x00 {
				errCh <- fmt.Errorf("ezsp: energyScan: complete with status 0x%02X", params[1])
			}
			return

		default:
			slog.Debug("ezsp: energyScan: unexpected callback",
				"frameID", fmt.Sprintf("0x%04X", frameID))
		}
	}
}

// runActiveScan reads active scan callbacks until scanCompleteHandler.
func (c *Client) runActiveScan(ctx context.Context, results chan<- NetworkScanResult, errCh chan<- error) {
	defer func() {
		close(results)
		close(errCh)
		c.mu.Lock()
		c.scanning = false
		c.mu.Unlock()
	}()

	for {
		raw, err := c.conn.Recv(ctx)
		if err != nil {
			errCh <- fmt.Errorf("ezsp: activeScan: recv: %w", err)
			return
		}

		frameID, params, err := c.decodeCallback(raw)
		if err != nil {
			slog.Debug("ezsp: activeScan: decode error, ignoring", "err", err)
			continue
		}

		switch frameID {
		case frameIDNetworkFoundHandler:
			// EmberZigbeeNetwork: channel(1) + panId(2) + extPanId(8) +
			//   allowingJoin(1) + stackProfile(1) + nwkUpdateId(1) = 14
			// Then: lastHopLqi(1) + lastHopRssi(1) = 2
			// Total: 16 bytes
			if len(params) < 16 {
				slog.Debug("ezsp: activeScan: result too short", "len", len(params))
				continue
			}
			var r NetworkScanResult
			r.Channel = params[0]
			r.PanID = binary.LittleEndian.Uint16(params[1:3])
			copy(r.ExtendedPanID[:], params[3:11])
			r.AllowingJoin = params[11] != 0
			r.StackProfile = params[12]
			r.NwkUpdateID = params[13]
			r.LQI = params[14]
			r.RSSI = int8(params[15])
			results <- r

		case frameIDScanCompleteHandler:
			if len(params) >= 2 && params[1] != 0x00 {
				errCh <- fmt.Errorf("ezsp: activeScan: complete with status 0x%02X", params[1])
			}
			return

		default:
			slog.Debug("ezsp: activeScan: unexpected callback",
				"frameID", fmt.Sprintf("0x%04X", frameID))
		}
	}
}

// decodeCallback decodes an EZSP callback frame and returns the frame ID and parameters.
func (c *Client) decodeCallback(raw []byte) (frameID uint16, params []byte, err error) {
	if c.extended {
		_, frameID, params, err = decodeExtended(raw)
	} else {
		_, frameID, params, err = decodeLegacy(raw)
	}
	return
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
