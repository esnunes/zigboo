package zdo

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/esnunes/zigboo/ezsp"
	"github.com/esnunes/zigboo/host"
)

// --- Mock transceiver ---

type sentMsg struct {
	destID   uint16
	apsFrame ezsp.EmberApsFrame
	payload  []byte
}

type mockTransceiver struct {
	sendErr  error
	sent     []sentMsg
	handlers map[uint32]func(msg host.IncomingMessage)
}

func newMockTransceiver() *mockTransceiver {
	return &mockTransceiver{
		handlers: make(map[uint32]func(msg host.IncomingMessage)),
	}
}

func (m *mockTransceiver) SendUnicast(_ context.Context, destID uint16, apsFrame ezsp.EmberApsFrame, payload []byte) error {
	if m.sendErr != nil {
		return m.sendErr
	}
	p := make([]byte, len(payload))
	copy(p, payload)
	m.sent = append(m.sent, sentMsg{destID, apsFrame, p})
	return nil
}

func (m *mockTransceiver) OnMessage(profileID, clusterID uint16, fn func(msg host.IncomingMessage)) {
	key := uint32(profileID)<<16 | uint32(clusterID)
	m.handlers[key] = fn
}

// inject simulates an incoming ZDO response by calling the registered handler.
func (m *mockTransceiver) inject(profileID, clusterID, senderID uint16, payload []byte) {
	key := uint32(profileID)<<16 | uint32(clusterID)
	if fn, ok := m.handlers[key]; ok {
		fn(host.IncomingMessage{
			SenderID: senderID,
			ApsFrame: ezsp.EmberApsFrame{
				ProfileID: profileID,
				ClusterID: clusterID,
			},
			Payload: payload,
		})
	}
}

// --- Type parsing tests ---

func TestParseNodeDescriptor(t *testing.T) {
	// 13-byte node descriptor for a router with manufacturer code 0x1234.
	data := make([]byte, 13)
	data[0] = 0x01                                          // logical type: router
	data[2] = 0x8E                                          // MAC capabilities
	binary.LittleEndian.PutUint16(data[3:5], 0x1234)        // manufacturer code
	data[5] = 80                                            // max buffer size
	binary.LittleEndian.PutUint16(data[6:8], 128)           // max incoming transfer
	binary.LittleEndian.PutUint16(data[8:10], 0x2040)       // server mask
	binary.LittleEndian.PutUint16(data[10:12], 128)         // max outgoing transfer

	nd, err := parseNodeDescriptor(data)
	if err != nil {
		t.Fatalf("parseNodeDescriptor() error = %v", err)
	}
	if nd.LogicalType != 1 {
		t.Errorf("LogicalType = %d, want 1 (router)", nd.LogicalType)
	}
	if nd.ManufacturerCode != 0x1234 {
		t.Errorf("ManufacturerCode = 0x%04X, want 0x1234", nd.ManufacturerCode)
	}
	if nd.MaxBufferSize != 80 {
		t.Errorf("MaxBufferSize = %d, want 80", nd.MaxBufferSize)
	}
}

func TestParseSimpleDescriptor(t *testing.T) {
	// Simple descriptor: endpoint 1, HA profile, device 0x0100,
	// 2 input clusters (0x0000, 0x0006), 1 output cluster (0x000A).
	data := make([]byte, 0, 20)
	data = append(data, 0x01)       // endpoint
	data = append(data, 0x04, 0x01) // profileID: 0x0104
	data = append(data, 0x00, 0x01) // deviceID: 0x0100
	data = append(data, 0x00)       // deviceVersion
	data = append(data, 0x02)       // input cluster count
	data = append(data, 0x00, 0x00) // Basic (0x0000)
	data = append(data, 0x06, 0x00) // On/Off (0x0006)
	data = append(data, 0x01)       // output cluster count
	data = append(data, 0x0A, 0x00) // Time (0x000A)

	sd, err := parseSimpleDescriptor(data)
	if err != nil {
		t.Fatalf("parseSimpleDescriptor() error = %v", err)
	}
	if sd.Endpoint != 1 {
		t.Errorf("Endpoint = %d, want 1", sd.Endpoint)
	}
	if sd.ProfileID != 0x0104 {
		t.Errorf("ProfileID = 0x%04X, want 0x0104", sd.ProfileID)
	}
	if sd.DeviceID != 0x0100 {
		t.Errorf("DeviceID = 0x%04X, want 0x0100", sd.DeviceID)
	}
	if len(sd.InputClusters) != 2 || sd.InputClusters[0] != 0x0000 || sd.InputClusters[1] != 0x0006 {
		t.Errorf("InputClusters = %v, want [0x0000, 0x0006]", sd.InputClusters)
	}
	if len(sd.OutputClusters) != 1 || sd.OutputClusters[0] != 0x000A {
		t.Errorf("OutputClusters = %v, want [0x000A]", sd.OutputClusters)
	}
}

// --- Client integration tests ---

func TestActiveEndpoints(t *testing.T) {
	mock := newMockTransceiver()
	c := New(mock)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	type result struct {
		eps []uint8
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		eps, err := c.ActiveEndpoints(ctx, 0x1234)
		resCh <- result{eps, err}
	}()

	// Wait for send, then inject response.
	time.Sleep(50 * time.Millisecond)

	// ZDO Active Endpoints Response: seq(1) + status(1) + addr(2) + count(1) + eps(N)
	zdoResp := []byte{0x00, 0x00, 0x34, 0x12, 0x02, 0x01, 0x02}
	mock.inject(ezsp.ProfileIDZDP, 0x8005, 0x1234, zdoResp)

	select {
	case res := <-resCh:
		if res.err != nil {
			t.Fatalf("ActiveEndpoints() error = %v", res.err)
		}
		if len(res.eps) != 2 || res.eps[0] != 1 || res.eps[1] != 2 {
			t.Errorf("ActiveEndpoints() = %v, want [1 2]", res.eps)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}

	// Verify the sent message.
	if len(mock.sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(mock.sent))
	}
	if mock.sent[0].destID != 0x1234 {
		t.Errorf("destID = 0x%04X, want 0x1234", mock.sent[0].destID)
	}
	if mock.sent[0].apsFrame.ClusterID != 0x0005 {
		t.Errorf("clusterID = 0x%04X, want 0x0005", mock.sent[0].apsFrame.ClusterID)
	}
}

func TestNodeDescriptor(t *testing.T) {
	mock := newMockTransceiver()
	c := New(mock)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	type result struct {
		nd  NodeDescriptor
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		nd, err := c.NodeDescriptor(ctx, 0x5678)
		resCh <- result{nd, err}
	}()

	time.Sleep(50 * time.Millisecond)

	// ZDO Node Descriptor Response: seq(1) + status(1) + addr(2) + nodeDescriptor(13)
	ndBytes := make([]byte, 13)
	ndBytes[0] = 0x02                                            // logical type: end device
	ndBytes[2] = 0x80                                            // MAC capabilities
	binary.LittleEndian.PutUint16(ndBytes[3:5], 0xABCD)         // manufacturer code
	ndBytes[5] = 64                                              // max buffer size
	binary.LittleEndian.PutUint16(ndBytes[6:8], 256)             // max incoming transfer
	binary.LittleEndian.PutUint16(ndBytes[8:10], 0x0000)         // server mask
	binary.LittleEndian.PutUint16(ndBytes[10:12], 256)           // max outgoing transfer

	zdoResp := make([]byte, 0, 17)
	zdoResp = append(zdoResp, 0x00, 0x00, 0x78, 0x56) // seq, status, addr
	zdoResp = append(zdoResp, ndBytes...)

	mock.inject(ezsp.ProfileIDZDP, 0x8002, 0x5678, zdoResp)

	select {
	case res := <-resCh:
		if res.err != nil {
			t.Fatalf("NodeDescriptor() error = %v", res.err)
		}
		if res.nd.LogicalType != 2 {
			t.Errorf("LogicalType = %d, want 2 (end device)", res.nd.LogicalType)
		}
		if res.nd.ManufacturerCode != 0xABCD {
			t.Errorf("ManufacturerCode = 0x%04X, want 0xABCD", res.nd.ManufacturerCode)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
}

func TestSimpleDescriptor(t *testing.T) {
	mock := newMockTransceiver()
	c := New(mock)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	type result struct {
		sd  SimpleDescriptor
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		sd, err := c.SimpleDescriptor(ctx, 0x1234, 1)
		resCh <- result{sd, err}
	}()

	time.Sleep(50 * time.Millisecond)

	// Build simple descriptor bytes.
	sdBytes := make([]byte, 0, 14)
	sdBytes = append(sdBytes, 0x01)       // endpoint
	sdBytes = append(sdBytes, 0x04, 0x01) // profileID: 0x0104
	sdBytes = append(sdBytes, 0x02, 0x01) // deviceID: 0x0102
	sdBytes = append(sdBytes, 0x00)       // deviceVersion
	sdBytes = append(sdBytes, 0x01)       // input cluster count: 1
	sdBytes = append(sdBytes, 0x00, 0x00) // Basic (0x0000)
	sdBytes = append(sdBytes, 0x01)       // output cluster count: 1
	sdBytes = append(sdBytes, 0x03, 0x00) // Identify (0x0003)

	// ZDO Simple Descriptor Response: seq(1) + status(1) + addr(2) + length(1) + sd(N)
	zdoResp := make([]byte, 0, 5+len(sdBytes))
	zdoResp = append(zdoResp, 0x00, 0x00, 0x34, 0x12) // seq, status, addr
	zdoResp = append(zdoResp, byte(len(sdBytes)))
	zdoResp = append(zdoResp, sdBytes...)

	mock.inject(ezsp.ProfileIDZDP, 0x8004, 0x1234, zdoResp)

	select {
	case res := <-resCh:
		if res.err != nil {
			t.Fatalf("SimpleDescriptor() error = %v", res.err)
		}
		if res.sd.Endpoint != 1 {
			t.Errorf("Endpoint = %d, want 1", res.sd.Endpoint)
		}
		if res.sd.ProfileID != 0x0104 {
			t.Errorf("ProfileID = 0x%04X, want 0x0104", res.sd.ProfileID)
		}
		if res.sd.DeviceID != 0x0102 {
			t.Errorf("DeviceID = 0x%04X, want 0x0102", res.sd.DeviceID)
		}
		if len(res.sd.InputClusters) != 1 || res.sd.InputClusters[0] != 0x0000 {
			t.Errorf("InputClusters = %v, want [0x0000]", res.sd.InputClusters)
		}
		if len(res.sd.OutputClusters) != 1 || res.sd.OutputClusters[0] != 0x0003 {
			t.Errorf("OutputClusters = %v, want [0x0003]", res.sd.OutputClusters)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
}

func TestRequestTimeout(t *testing.T) {
	mock := newMockTransceiver()
	c := New(mock)

	// Very short timeout — no response will arrive.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := c.ActiveEndpoints(ctx, 0x1234)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestResponseMismatchIgnored(t *testing.T) {
	mock := newMockTransceiver()
	c := New(mock)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	type result struct {
		eps []uint8
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		eps, err := c.ActiveEndpoints(ctx, 0x1234)
		resCh <- result{eps, err}
	}()

	time.Sleep(50 * time.Millisecond)

	// Inject a response with wrong sender — should be ignored.
	mock.inject(ezsp.ProfileIDZDP, 0x8005, 0x5678, []byte{0x00, 0x00, 0x78, 0x56, 0x01, 0x01})

	// Now inject correct response.
	mock.inject(ezsp.ProfileIDZDP, 0x8005, 0x1234, []byte{0x00, 0x00, 0x34, 0x12, 0x01, 0x03})

	select {
	case res := <-resCh:
		if res.err != nil {
			t.Fatalf("ActiveEndpoints() error = %v", res.err)
		}
		if len(res.eps) != 1 || res.eps[0] != 3 {
			t.Errorf("ActiveEndpoints() = %v, want [3]", res.eps)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
}
