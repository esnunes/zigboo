// Package zcl implements enough of the Zigbee Cluster Library to read
// Basic cluster attributes from devices during pairing.
package zcl

import (
	"context"
	"fmt"
	"sync"

	"github.com/esnunes/zigboo/ezsp"
	"github.com/esnunes/zigboo/host"
)

// Transceiver abstracts the host for sending and receiving ZCL messages.
type Transceiver interface {
	SendUnicast(ctx context.Context, destID uint16, apsFrame ezsp.EmberApsFrame, payload []byte) error
	OnMessage(profileID, clusterID uint16, fn func(msg host.IncomingMessage))
}

// pendingKey identifies an in-flight ZCL request.
type pendingKey struct {
	addr uint16
	seq  uint8
}

// Client sends ZCL commands and correlates responses.
type Client struct {
	tr         Transceiver
	seq        uint8
	mu         sync.Mutex
	pending    map[pendingKey]chan []byte
	registered map[uint16]bool // cluster IDs with registered handlers
}

// New creates a ZCL client.
func New(tr Transceiver) *Client {
	return &Client{
		tr:         tr,
		pending:    make(map[pendingKey]chan []byte),
		registered: make(map[uint16]bool),
	}
}

// ReadAttributes reads ZCL attributes from a remote device's cluster.
func (c *Client) ReadAttributes(ctx context.Context, addr uint16, srcEndpoint, dstEndpoint uint8, clusterID uint16, attrIDs []uint16) (map[uint16]AttributeValue, error) {
	c.ensureHandler(clusterID)

	seq := c.nextSeq()
	frame := encodeReadAttributes(seq, attrIDs)

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

	// Send the ZCL request.
	apsFrame := ezsp.EmberApsFrame{
		ProfileID:           ezsp.ProfileIDHA,
		ClusterID:           clusterID,
		SourceEndpoint:      srcEndpoint,
		DestinationEndpoint: dstEndpoint,
		Options:             ezsp.APSOptionEnableRouteDiscovery | ezsp.APSOptionRetry,
	}
	if err := c.tr.SendUnicast(ctx, addr, apsFrame, frame); err != nil {
		return nil, fmt.Errorf("zcl: readAttributes: %w", err)
	}

	// Wait for correlated response.
	select {
	case resp := <-ch:
		return decodeReadAttributesResponse(resp)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ensureHandler registers a message handler for the given cluster if not already registered.
func (c *Client) ensureHandler(clusterID uint16) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.registered[clusterID] {
		return
	}
	c.tr.OnMessage(ezsp.ProfileIDHA, clusterID, c.handleResponse)
	c.registered[clusterID] = true
}

// handleResponse routes an incoming ZCL response to the matching pending request.
func (c *Client) handleResponse(msg host.IncomingMessage) {
	if len(msg.Payload) < 3 {
		return
	}
	// Verify this is a Read Attributes Response.
	if msg.Payload[2] != cmdReadAttributesResponse {
		return
	}
	zclSeq := msg.Payload[1]
	key := pendingKey{msg.SenderID, zclSeq}

	c.mu.Lock()
	ch, ok := c.pending[key]
	if ok {
		delete(c.pending, key)
	}
	c.mu.Unlock()

	if ok {
		ch <- msg.Payload
	}
}

// nextSeq returns the next ZCL sequence number.
func (c *Client) nextSeq() uint8 {
	c.mu.Lock()
	defer c.mu.Unlock()
	seq := c.seq
	c.seq++
	return seq
}
