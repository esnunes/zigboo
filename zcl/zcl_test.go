package zcl

import (
	"context"
	"encoding/binary"
	"sync"
	"testing"
	"time"

	"github.com/esnunes/zigboo/ezsp"
	"github.com/esnunes/zigboo/host"
)

// --- Mock transceiver ---

type mockTransceiver struct {
	sendErr  error
	mu       sync.RWMutex
	handlers map[uint32]func(msg host.IncomingMessage)
}

func newMockTransceiver() *mockTransceiver {
	return &mockTransceiver{
		handlers: make(map[uint32]func(msg host.IncomingMessage)),
	}
}

func (m *mockTransceiver) SendUnicast(_ context.Context, _ uint16, _ ezsp.EmberApsFrame, _ []byte) error {
	return m.sendErr
}

func (m *mockTransceiver) OnMessage(profileID, clusterID uint16, fn func(msg host.IncomingMessage)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := uint32(profileID)<<16 | uint32(clusterID)
	m.handlers[key] = fn
}

func (m *mockTransceiver) inject(profileID, clusterID, senderID uint16, payload []byte) {
	m.mu.RLock()
	key := uint32(profileID)<<16 | uint32(clusterID)
	fn, ok := m.handlers[key]
	m.mu.RUnlock()
	if ok {
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

// --- Frame encoding tests ---

func TestEncodeReadAttributes(t *testing.T) {
	frame := encodeReadAttributes(0x05, []uint16{AttrManufacturerName, AttrModelIdentifier})

	// FC(1) + seq(1) + cmd(1) + 2 attr IDs(4) = 7
	if len(frame) != 7 {
		t.Fatalf("frame length = %d, want 7", len(frame))
	}
	if frame[0] != fcDisableDefaultResponse {
		t.Errorf("FC = 0x%02X, want 0x%02X", frame[0], fcDisableDefaultResponse)
	}
	if frame[1] != 0x05 {
		t.Errorf("seq = %d, want 5", frame[1])
	}
	if frame[2] != cmdReadAttributes {
		t.Errorf("cmd = 0x%02X, want 0x%02X", frame[2], cmdReadAttributes)
	}
	if binary.LittleEndian.Uint16(frame[3:5]) != AttrManufacturerName {
		t.Errorf("attr[0] = 0x%04X, want 0x%04X", binary.LittleEndian.Uint16(frame[3:5]), AttrManufacturerName)
	}
	if binary.LittleEndian.Uint16(frame[5:7]) != AttrModelIdentifier {
		t.Errorf("attr[1] = 0x%04X, want 0x%04X", binary.LittleEndian.Uint16(frame[5:7]), AttrModelIdentifier)
	}
}

// --- Response decoding tests ---

func TestDecodeReadAttributesResponse(t *testing.T) {
	// Build a response with two string attributes + one unsupported.
	resp := []byte{
		0x18,                   // FC: server→client, disable default response
		0x05,                   // seq
		cmdReadAttributesResponse,
	}

	// Attr 0x0004: Manufacturer Name = "IKEA"
	resp = append(resp, 0x04, 0x00) // attrID
	resp = append(resp, 0x00)       // status: success
	resp = append(resp, DataTypeCharString)
	resp = append(resp, 4)                             // string length
	resp = append(resp, 'I', 'K', 'E', 'A')

	// Attr 0x0005: Model Identifier = "E1810"
	resp = append(resp, 0x05, 0x00)
	resp = append(resp, 0x00)
	resp = append(resp, DataTypeCharString)
	resp = append(resp, 5)
	resp = append(resp, 'E', '1', '8', '1', '0')

	// Attr 0x4000: SW Build ID = unsupported (0x86)
	resp = append(resp, 0x00, 0x40)
	resp = append(resp, 0x86) // UNSUPPORTED_ATTRIBUTE

	result, err := decodeReadAttributesResponse(resp)
	if err != nil {
		t.Fatalf("decodeReadAttributesResponse() error = %v", err)
	}

	if v, ok := result[AttrManufacturerName]; !ok || v.Value != "IKEA" {
		t.Errorf("ManufacturerName = %v, want 'IKEA'", v)
	}
	if v, ok := result[AttrModelIdentifier]; !ok || v.Value != "E1810" {
		t.Errorf("ModelIdentifier = %v, want 'E1810'", v)
	}
	if v, ok := result[AttrSWBuildID]; !ok || v.Status != 0x86 {
		t.Errorf("SWBuildID status = 0x%02X, want 0x86", v.Status)
	}
}

// --- Client integration tests ---

func TestReadAttributes(t *testing.T) {
	mock := newMockTransceiver()
	c := New(mock)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	type result struct {
		attrs map[uint16]AttributeValue
		err   error
	}
	resCh := make(chan result, 1)
	go func() {
		attrs, err := c.ReadAttributes(ctx, 0x1234, 1, 1, BasicClusterID,
			[]uint16{AttrManufacturerName, AttrModelIdentifier})
		resCh <- result{attrs, err}
	}()

	time.Sleep(50 * time.Millisecond)

	// Build ZCL Read Attributes Response.
	resp := []byte{0x18, 0x00, cmdReadAttributesResponse}
	// Attr 0x0004: "TestMfr"
	resp = append(resp, 0x04, 0x00, 0x00, DataTypeCharString, 7)
	resp = append(resp, "TestMfr"...)
	// Attr 0x0005: "Model1"
	resp = append(resp, 0x05, 0x00, 0x00, DataTypeCharString, 6)
	resp = append(resp, "Model1"...)

	mock.inject(ezsp.ProfileIDHA, BasicClusterID, 0x1234, resp)

	select {
	case res := <-resCh:
		if res.err != nil {
			t.Fatalf("ReadAttributes() error = %v", res.err)
		}
		if v := res.attrs[AttrManufacturerName]; v.Value != "TestMfr" {
			t.Errorf("ManufacturerName = %v, want 'TestMfr'", v.Value)
		}
		if v := res.attrs[AttrModelIdentifier]; v.Value != "Model1" {
			t.Errorf("ModelIdentifier = %v, want 'Model1'", v.Value)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
}

func TestReadAttributesTimeout(t *testing.T) {
	mock := newMockTransceiver()
	c := New(mock)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := c.ReadAttributes(ctx, 0x1234, 1, 1, BasicClusterID,
		[]uint16{AttrManufacturerName})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestReadAttributesPartialSuccess(t *testing.T) {
	mock := newMockTransceiver()
	c := New(mock)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	type result struct {
		attrs map[uint16]AttributeValue
		err   error
	}
	resCh := make(chan result, 1)
	go func() {
		attrs, err := c.ReadAttributes(ctx, 0x1234, 1, 1, BasicClusterID,
			[]uint16{AttrManufacturerName, AttrSWBuildID})
		resCh <- result{attrs, err}
	}()

	time.Sleep(50 * time.Millisecond)

	// Response: ManufacturerName=success, SWBuildID=unsupported
	resp := []byte{0x18, 0x00, cmdReadAttributesResponse}
	resp = append(resp, 0x04, 0x00, 0x00, DataTypeCharString, 4)
	resp = append(resp, "ACME"...)
	resp = append(resp, 0x00, 0x40, 0x86) // SWBuildID unsupported

	mock.inject(ezsp.ProfileIDHA, BasicClusterID, 0x1234, resp)

	select {
	case res := <-resCh:
		if res.err != nil {
			t.Fatalf("ReadAttributes() error = %v", res.err)
		}
		if v := res.attrs[AttrManufacturerName]; v.Value != "ACME" {
			t.Errorf("ManufacturerName = %v, want 'ACME'", v.Value)
		}
		if v := res.attrs[AttrSWBuildID]; v.Status != 0x86 {
			t.Errorf("SWBuildID status = 0x%02X, want 0x86", v.Status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
}
