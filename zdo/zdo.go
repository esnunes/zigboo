// Package zdo implements Zigbee Device Object requests for device interview.
//
// ZDO requests are APS unicast messages to endpoint 0 with profile 0x0000.
// Responses arrive as incoming messages with cluster ID = request cluster + 0x8000.
package zdo

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/esnunes/zigboo/ezsp"
	"github.com/esnunes/zigboo/host"
)

// ZDO cluster IDs.
const (
	clusterNodeDescriptor   uint16 = 0x0002
	clusterSimpleDescriptor uint16 = 0x0004
	clusterActiveEndpoints  uint16 = 0x0005
)

var (
	errResponseTooShort = errors.New("zdo: response too short")
	errZDOStatus        = errors.New("zdo: non-success status")
)

// Transceiver abstracts the host for sending and receiving ZDO messages.
type Transceiver interface {
	SendUnicast(ctx context.Context, destID uint16, apsFrame ezsp.EmberApsFrame, payload []byte) error
	OnMessage(profileID, clusterID uint16, fn func(msg host.IncomingMessage))
}

// pendingKey identifies an in-flight ZDO request.
type pendingKey struct {
	addr uint16
	seq  uint8
}

// Client sends ZDO requests and correlates responses.
type Client struct {
	tr      Transceiver
	seq     uint8
	mu      sync.Mutex
	pending map[pendingKey]chan []byte
}

// New creates a ZDO client and registers response handlers with the host.
func New(tr Transceiver) *Client {
	c := &Client{
		tr:      tr,
		pending: make(map[pendingKey]chan []byte),
	}
	tr.OnMessage(ezsp.ProfileIDZDP, clusterNodeDescriptor|0x8000, c.handleResponse)
	tr.OnMessage(ezsp.ProfileIDZDP, clusterSimpleDescriptor|0x8000, c.handleResponse)
	tr.OnMessage(ezsp.ProfileIDZDP, clusterActiveEndpoints|0x8000, c.handleResponse)
	return c
}

// NodeDescriptor requests and parses the node descriptor from a remote device.
func (c *Client) NodeDescriptor(ctx context.Context, addr uint16) (NodeDescriptor, error) {
	resp, err := c.request(ctx, addr, clusterNodeDescriptor, []byte{byte(addr), byte(addr >> 8)})
	if err != nil {
		return NodeDescriptor{}, fmt.Errorf("zdo: nodeDescriptor: %w", err)
	}
	// Response: status(1) + nwkAddr(2) + nodeDescriptor(13)
	if len(resp) < 16 {
		return NodeDescriptor{}, fmt.Errorf("zdo: nodeDescriptor: %w", errResponseTooShort)
	}
	if resp[0] != 0x00 {
		return NodeDescriptor{}, fmt.Errorf("zdo: nodeDescriptor: status 0x%02X", resp[0])
	}
	return parseNodeDescriptor(resp[3:16])
}

// ActiveEndpoints requests and parses the active endpoint list from a remote device.
func (c *Client) ActiveEndpoints(ctx context.Context, addr uint16) ([]uint8, error) {
	resp, err := c.request(ctx, addr, clusterActiveEndpoints, []byte{byte(addr), byte(addr >> 8)})
	if err != nil {
		return nil, fmt.Errorf("zdo: activeEndpoints: %w", err)
	}
	// Response: status(1) + nwkAddr(2) + epCount(1) + eps(N)
	if len(resp) < 4 {
		return nil, fmt.Errorf("zdo: activeEndpoints: %w", errResponseTooShort)
	}
	if resp[0] != 0x00 {
		return nil, fmt.Errorf("zdo: activeEndpoints: status 0x%02X", resp[0])
	}
	count := int(resp[3])
	if len(resp) < 4+count {
		return nil, fmt.Errorf("zdo: activeEndpoints: %w", errResponseTooShort)
	}
	eps := make([]uint8, count)
	copy(eps, resp[4:4+count])
	return eps, nil
}

// SimpleDescriptor requests and parses a simple descriptor for an endpoint on a remote device.
func (c *Client) SimpleDescriptor(ctx context.Context, addr uint16, endpoint uint8) (SimpleDescriptor, error) {
	resp, err := c.request(ctx, addr, clusterSimpleDescriptor, []byte{byte(addr), byte(addr >> 8), endpoint})
	if err != nil {
		return SimpleDescriptor{}, fmt.Errorf("zdo: simpleDescriptor: %w", err)
	}
	// Response: status(1) + nwkAddr(2) + length(1) + simpleDescriptor(length)
	if len(resp) < 4 {
		return SimpleDescriptor{}, fmt.Errorf("zdo: simpleDescriptor: %w", errResponseTooShort)
	}
	if resp[0] != 0x00 {
		return SimpleDescriptor{}, fmt.Errorf("zdo: simpleDescriptor: status 0x%02X", resp[0])
	}
	sdLen := int(resp[3])
	if len(resp) < 4+sdLen {
		return SimpleDescriptor{}, fmt.Errorf("zdo: simpleDescriptor: %w", errResponseTooShort)
	}
	return parseSimpleDescriptor(resp[4 : 4+sdLen])
}

// request sends a ZDO request and waits for the correlated response.
func (c *Client) request(ctx context.Context, addr uint16, clusterID uint16, payload []byte) ([]byte, error) {
	seq := c.nextSeq()

	// Prepend ZDO sequence number to payload.
	frame := make([]byte, 1+len(payload))
	frame[0] = seq
	copy(frame[1:], payload)

	// Register pending response.
	ch := make(chan []byte, 1)
	key := pendingKey{addr, seq}
	c.mu.Lock()
	c.pending[key] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
	}()

	// Send the ZDO request.
	apsFrame := ezsp.EmberApsFrame{
		ProfileID:           ezsp.ProfileIDZDP,
		ClusterID:           clusterID,
		SourceEndpoint:      0,
		DestinationEndpoint: 0,
		Options:             ezsp.APSOptionEnableRouteDiscovery | ezsp.APSOptionRetry,
	}
	if err := c.tr.SendUnicast(ctx, addr, apsFrame, frame); err != nil {
		return nil, err
	}

	// Wait for correlated response.
	select {
	case resp := <-ch:
		// resp is the ZDO payload starting after the sequence byte.
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// handleResponse routes an incoming ZDO response to the matching pending request.
func (c *Client) handleResponse(msg host.IncomingMessage) {
	if len(msg.Payload) < 1 {
		return
	}
	zdoSeq := msg.Payload[0]
	key := pendingKey{msg.SenderID, zdoSeq}

	c.mu.Lock()
	ch, ok := c.pending[key]
	if ok {
		delete(c.pending, key)
	}
	c.mu.Unlock()

	if ok {
		// Send everything after the sequence byte.
		ch <- msg.Payload[1:]
	}
}

// nextSeq returns the next ZDO transaction sequence number.
func (c *Client) nextSeq() uint8 {
	c.mu.Lock()
	defer c.mu.Unlock()
	seq := c.seq
	c.seq++
	return seq
}
