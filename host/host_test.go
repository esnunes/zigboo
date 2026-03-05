package host

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/esnunes/zigboo/ash"
	"github.com/esnunes/zigboo/ezsp"
)

// --- Test helpers: mock serial port and ASH frame construction ---
// These are intentionally independent implementations of ASH framing,
// serving as cross-reference validation (per the ASH encoding bug postmortem).

type mockPort struct {
	mu       sync.Mutex
	readBuf  *bytes.Buffer
	writeBuf *bytes.Buffer
	closed   bool
	onWrite  func(data []byte)
}

func newMockPort() *mockPort {
	return &mockPort{
		readBuf:  &bytes.Buffer{},
		writeBuf: &bytes.Buffer{},
	}
}

func (m *mockPort) Read(buf []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, io.EOF
	}
	if m.readBuf.Len() == 0 {
		m.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
		m.mu.Lock()
		if m.readBuf.Len() == 0 {
			return 0, nil
		}
	}
	return m.readBuf.Read(buf)
}

func (m *mockPort) Write(buf []byte) (int, error) {
	m.mu.Lock()
	n, err := m.writeBuf.Write(buf)
	cb := m.onWrite
	m.mu.Unlock()
	if cb != nil {
		cb(buf)
	}
	return n, err
}

func (m *mockPort) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *mockPort) Flush() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.readBuf.Reset()
	return nil
}

func (m *mockPort) injectFrame(data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.readBuf.Write(data)
}

// ASH framing helpers (independent implementation for cross-reference).

func testCRC(data []byte) uint16 {
	var table [256]uint16
	for i := range 256 {
		crc := uint16(i) << 8
		for range 8 {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ 0x1021
			} else {
				crc <<= 1
			}
		}
		table[i] = crc
	}
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc = (crc << 8) ^ table[byte(crc>>8)^b]
	}
	return crc
}

func testRandomize(data []byte) {
	lfsr := byte(0x42)
	for i := range data {
		data[i] ^= lfsr
		lsb := lfsr & 0x01
		lfsr >>= 1
		if lsb != 0 {
			lfsr ^= 0xB8
		}
	}
}

func testStuff(data []byte) []byte {
	out := make([]byte, 0, len(data)*2)
	for _, b := range data {
		switch b {
		case 0x7E, 0x7D, 0x11, 0x13, 0x18, 0x1A:
			out = append(out, 0x7D, b^0x20)
		default:
			out = append(out, b)
		}
	}
	return out
}

func testEncodeDataFrame(control byte, payload []byte) []byte {
	randData := make([]byte, len(payload))
	copy(randData, payload)
	testRandomize(randData)
	frame := make([]byte, 0, 1+len(payload)+2)
	frame = append(frame, control)
	frame = append(frame, randData...)
	crc := testCRC(frame)
	frame = append(frame, byte(crc>>8), byte(crc))
	frame = testStuff(frame)
	frame = append(frame, 0x7E)
	return frame
}

func testEncodeACK(ackNum byte) []byte {
	control := byte(0x80) | (ackNum & 0x07)
	frame := []byte{control}
	crc := testCRC(frame)
	frame = append(frame, byte(crc>>8), byte(crc))
	frame = testStuff(frame)
	frame = append(frame, 0x7E)
	return frame
}

func testEncodeRSTACK(version, resetCode byte) []byte {
	raw := []byte{0xC1, version, resetCode}
	crc := testCRC(raw)
	raw = append(raw, byte(crc>>8), byte(crc))
	raw = testStuff(raw)
	raw = append(raw, 0x7E)
	return raw
}

func testDataControl(frmNum, ackNum byte) byte {
	return (frmNum & 0x07) << 4 | (ackNum & 0x07)
}

// setupMockNCP creates a mock NCP that responds to EZSP commands.
// Returns an ASH Conn ready for use after RST/RSTACK handshake, plus the mock port.
func setupMockNCP(t *testing.T, responses [][]byte) (*ash.Conn, *mockPort) {
	t.Helper()
	mp := newMockPort()

	var (
		mu         sync.Mutex
		handshook  bool
		respIdx    int
		ncpFrmNum  byte
		hostFrmNum byte
	)

	mp.onWrite = func(data []byte) {
		mu.Lock()
		defer mu.Unlock()

		if !handshook {
			if bytes.Contains(data, []byte{0x7E}) {
				handshook = true
				go func() {
					time.Sleep(5 * time.Millisecond)
					mp.injectFrame(testEncodeRSTACK(0x0B, 0x01))
				}()
			}
			return
		}

		if !bytes.Contains(data, []byte{0x7E}) {
			return
		}

		// Skip ACK frames (short).
		if len(data) < 8 {
			return
		}

		go func() {
			time.Sleep(5 * time.Millisecond)

			mu.Lock()
			hostFrmNum = (hostFrmNum + 1) & 0x07
			ackFrame := testEncodeACK(hostFrmNum)

			idx := respIdx
			respIdx++
			frmNum := ncpFrmNum
			ncpFrmNum = (ncpFrmNum + 1) & 0x07
			mu.Unlock()

			mp.injectFrame(ackFrame)

			if idx < len(responses) {
				control := testDataControl(frmNum, hostFrmNum)
				mp.injectFrame(testEncodeDataFrame(control, responses[idx]))
			}
		}()
	}

	conn := ash.New(mp)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, _, err := conn.Reset(ctx); err != nil {
		t.Fatalf("mock NCP reset failed: %v", err)
	}

	t.Cleanup(func() {
		conn.Close()
	})

	return conn, mp
}

// injectCallback injects an unsolicited callback DATA frame from the NCP.
func injectCallback(mp *mockPort, ncpFrmNum, ackNum byte, ezspFrame []byte) {
	control := testDataControl(ncpFrmNum, ackNum)
	mp.injectFrame(testEncodeDataFrame(control, ezspFrame))
}

// --- Tests ---

func TestCommand(t *testing.T) {
	// networkState command (0x0018), response: status=0x02 (joined)
	resp := ezsp.EncodeExtended(0, 0x0018, []byte{0x02})
	conn, _ := setupMockNCP(t, [][]byte{resp})

	h := New(conn, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h.Start(ctx)
	defer h.Close()

	params, err := h.Command(ctx, 0x0018, nil)
	if err != nil {
		t.Fatalf("Command() error = %v", err)
	}
	if len(params) < 1 || params[0] != 0x02 {
		t.Errorf("Command() params = %x, want [02]", params)
	}
}

func TestCommand_MultipleSequential(t *testing.T) {
	// Two commands: networkState + getNodeId
	resp1 := ezsp.EncodeExtended(0, 0x0018, []byte{0x02})
	resp2 := ezsp.EncodeExtended(0, 0x0027, []byte{0x00, 0x00})
	conn, _ := setupMockNCP(t, [][]byte{resp1, resp2})

	h := New(conn, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h.Start(ctx)
	defer h.Close()

	params1, err := h.Command(ctx, 0x0018, nil)
	if err != nil {
		t.Fatalf("Command(networkState) error = %v", err)
	}
	if len(params1) < 1 || params1[0] != 0x02 {
		t.Errorf("networkState params = %x, want [02]", params1)
	}

	params2, err := h.Command(ctx, 0x0027, nil)
	if err != nil {
		t.Fatalf("Command(getNodeId) error = %v", err)
	}
	if len(params2) < 2 {
		t.Errorf("getNodeId params too short: %x", params2)
	}
}

func TestCallbackDispatch(t *testing.T) {
	conn, mp := setupMockNCP(t, nil)

	h := New(conn, true)

	callbackReceived := make(chan []byte, 1)
	h.OnCallback(ezsp.FrameIDStackStatusHandler, func(params []byte) {
		p := make([]byte, len(params))
		copy(p, params)
		callbackReceived <- p
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h.Start(ctx)
	defer h.Close()

	// Inject an unsolicited stackStatusHandler callback.
	// NCP frmNum=0, host ackNum=0 (no commands sent yet, but after reset
	// the NCP's frmNum starts at 0 and host's ackNum starts at 0).
	cbFrame := ezsp.EncodeExtended(0, ezsp.FrameIDStackStatusHandler, []byte{0x02})
	injectCallback(mp, 0, 0, cbFrame)

	select {
	case params := <-callbackReceived:
		if len(params) < 1 || params[0] != 0x02 {
			t.Errorf("callback params = %x, want [02]", params)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for callback")
	}
}

func TestCallbackDuringCommand(t *testing.T) {
	// The mock NCP will send a callback before the command response.
	// We create a custom mock that injects a callback between ACK and response.
	mp := newMockPort()

	var (
		mu         sync.Mutex
		handshook  bool
		ncpFrmNum  byte
		hostFrmNum byte
	)

	mp.onWrite = func(data []byte) {
		mu.Lock()
		defer mu.Unlock()

		if !handshook {
			if bytes.Contains(data, []byte{0x7E}) {
				handshook = true
				go func() {
					time.Sleep(5 * time.Millisecond)
					mp.injectFrame(testEncodeRSTACK(0x0B, 0x01))
				}()
			}
			return
		}

		if !bytes.Contains(data, []byte{0x7E}) || len(data) < 8 {
			return
		}

		go func() {
			time.Sleep(5 * time.Millisecond)

			mu.Lock()
			hostFrmNum = (hostFrmNum + 1) & 0x07

			// Send ACK
			ackFrame := testEncodeACK(hostFrmNum)

			// First: send a callback (stackStatusHandler)
			cbFrmNum := ncpFrmNum
			ncpFrmNum = (ncpFrmNum + 1) & 0x07
			cbFrame := ezsp.EncodeExtended(0, ezsp.FrameIDStackStatusHandler, []byte{0x02})

			// Then: send the actual command response (networkState)
			respFrmNum := ncpFrmNum
			ncpFrmNum = (ncpFrmNum + 1) & 0x07
			respFrame := ezsp.EncodeExtended(0, 0x0018, []byte{0x02})
			mu.Unlock()

			mp.injectFrame(ackFrame)

			// Inject callback as the first DATA frame (this is what Send() returns).
			cbControl := testDataControl(cbFrmNum, hostFrmNum)
			mp.injectFrame(testEncodeDataFrame(cbControl, cbFrame))

			// Then inject the actual response (this is what Recv() returns).
			time.Sleep(5 * time.Millisecond)
			respControl := testDataControl(respFrmNum, hostFrmNum)
			mp.injectFrame(testEncodeDataFrame(respControl, respFrame))
		}()
	}

	conn := ash.New(mp)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := conn.Reset(ctx); err != nil {
		t.Fatalf("mock NCP reset failed: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	h := New(conn, true)

	callbackReceived := make(chan []byte, 1)
	h.OnCallback(ezsp.FrameIDStackStatusHandler, func(params []byte) {
		p := make([]byte, len(params))
		copy(p, params)
		callbackReceived <- p
	})

	h.Start(ctx)
	defer h.Close()

	// Send command — should receive callback + response.
	params, err := h.Command(ctx, 0x0018, nil)
	if err != nil {
		t.Fatalf("Command() error = %v", err)
	}
	if len(params) < 1 || params[0] != 0x02 {
		t.Errorf("Command() params = %x, want [02]", params)
	}

	// Verify callback was dispatched.
	select {
	case cbParams := <-callbackReceived:
		if len(cbParams) < 1 || cbParams[0] != 0x02 {
			t.Errorf("callback params = %x, want [02]", cbParams)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for callback dispatch")
	}
}

func TestClose(t *testing.T) {
	conn, _ := setupMockNCP(t, nil)

	h := New(conn, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h.Start(ctx)

	// Close should return promptly.
	done := make(chan struct{})
	go func() {
		h.Close()
		close(done)
	}()

	select {
	case <-done:
		// OK
	case <-time.After(3 * time.Second):
		t.Fatal("Close() did not return in time")
	}
}

func TestCommand_ContextCancelled(t *testing.T) {
	// No responses queued — command will hang until context is cancelled.
	conn, _ := setupMockNCP(t, nil)

	h := New(conn, true)
	bgCtx, bgCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer bgCancel()
	h.Start(bgCtx)
	defer h.Close()

	// Use a very short timeout for the command.
	cmdCtx, cmdCancel := context.WithTimeout(bgCtx, 100*time.Millisecond)
	defer cmdCancel()

	_, err := h.Command(cmdCtx, 0x0018, nil)
	if err == nil {
		t.Fatal("Command() expected error, got nil")
	}
}

func TestAddEndpoint(t *testing.T) {
	// addEndpoint response: EzspStatus=0x00 (success)
	resp := ezsp.EncodeExtended(0, ezsp.FrameIDAddEndpoint, []byte{0x00})
	conn, _ := setupMockNCP(t, [][]byte{resp})

	h := New(conn, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h.Start(ctx)
	defer h.Close()

	err := h.AddEndpoint(ctx, 1, ezsp.ProfileIDHA, 0x0005,
		[]uint16{0x0000},            // input: Basic
		[]uint16{0x0000, 0x0003},    // output: Basic, Identify
	)
	if err != nil {
		t.Fatalf("AddEndpoint() error = %v", err)
	}
}

func TestSendUnicast(t *testing.T) {
	// sendUnicast response: EmberStatus=0x00 (success)
	resp := ezsp.EncodeExtended(0, ezsp.FrameIDSendUnicast, []byte{0x00})
	conn, _ := setupMockNCP(t, [][]byte{resp})

	h := New(conn, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h.Start(ctx)
	defer h.Close()

	apsFrame := ezsp.EmberApsFrame{
		ProfileID:           ezsp.ProfileIDZDP,
		ClusterID:           0x0005, // Active Endpoints Request
		SourceEndpoint:      0,
		DestinationEndpoint: 0,
		Options:             ezsp.APSOptionEnableRouteDiscovery | ezsp.APSOptionRetry,
	}
	payload := []byte{0x01, 0x34, 0x12} // ZDO: seq=1, addr=0x1234
	err := h.SendUnicast(ctx, 0x1234, apsFrame, payload)
	if err != nil {
		t.Fatalf("SendUnicast() error = %v", err)
	}
}

func TestIncomingMessageRouting(t *testing.T) {
	conn, mp := setupMockNCP(t, nil)

	h := New(conn, true)

	msgReceived := make(chan IncomingMessage, 1)
	h.OnMessage(ezsp.ProfileIDZDP, 0x8005, func(msg IncomingMessage) {
		msgReceived <- msg
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h.Start(ctx)
	defer h.Close()

	// Build incomingMessageHandler callback params:
	// type(1) + apsFrame(11) + lqi(1) + rssi(1) + sender(2) + bindingIndex(1) + addressIndex(1) + msgLen(1) + msg(N)
	apsFrame := ezsp.EncodeApsFrame(ezsp.EmberApsFrame{
		ProfileID:           ezsp.ProfileIDZDP,
		ClusterID:           0x8005, // Active Endpoints Response
		SourceEndpoint:      0,
		DestinationEndpoint: 0,
	})
	zdoPayload := []byte{0x01, 0x00, 0x34, 0x12, 0x02, 0x01, 0x02} // seq, status, addr, count, ep1, ep2
	cbParams := make([]byte, 0, 19+len(zdoPayload))
	cbParams = append(cbParams, 0x00)           // type: UNICAST
	cbParams = append(cbParams, apsFrame...)     // APS frame (11 bytes)
	cbParams = append(cbParams, 0xFF)            // LQI
	cbParams = append(cbParams, 0xD0)            // RSSI (-48)
	cbParams = append(cbParams, 0x34, 0x12)      // sender node ID: 0x1234
	cbParams = append(cbParams, 0xFF)            // bindingIndex
	cbParams = append(cbParams, 0xFF)            // addressIndex
	cbParams = append(cbParams, byte(len(zdoPayload)))
	cbParams = append(cbParams, zdoPayload...)

	cbFrame := ezsp.EncodeExtended(0, ezsp.FrameIDIncomingMessageHandler, cbParams)
	injectCallback(mp, 0, 0, cbFrame)

	select {
	case msg := <-msgReceived:
		if msg.SenderID != 0x1234 {
			t.Errorf("SenderID = 0x%04X, want 0x1234", msg.SenderID)
		}
		if msg.ApsFrame.ClusterID != 0x8005 {
			t.Errorf("ClusterID = 0x%04X, want 0x8005", msg.ApsFrame.ClusterID)
		}
		if msg.LQI != 0xFF {
			t.Errorf("LQI = %d, want 255", msg.LQI)
		}
		if len(msg.Payload) != len(zdoPayload) {
			t.Errorf("Payload len = %d, want %d", len(msg.Payload), len(zdoPayload))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for incoming message")
	}
}

func TestMessageSentHandler(t *testing.T) {
	// Verify messageSentHandler doesn't panic on valid input.
	conn, mp := setupMockNCP(t, nil)

	h := New(conn, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h.Start(ctx)
	defer h.Close()

	// Build messageSentHandler callback params:
	// type(1) + indexOrDest(2) + apsFrame(11) + tag(1) + status(1) + msgLen(1) + msg(N)
	apsBytes := ezsp.EncodeApsFrame(ezsp.EmberApsFrame{
		ProfileID: ezsp.ProfileIDZDP,
		ClusterID: 0x0005,
	})
	cbParams := make([]byte, 0, 17)
	cbParams = append(cbParams, 0x00)       // type: DIRECT
	cbParams = append(cbParams, 0x34, 0x12) // destination: 0x1234
	cbParams = append(cbParams, apsBytes...) // APS frame (11 bytes)
	cbParams = append(cbParams, 0x00)       // tag
	cbParams = append(cbParams, 0x00)       // status: success
	cbParams = append(cbParams, 0x00)       // msgLen: 0

	cbFrame := ezsp.EncodeExtended(0, ezsp.FrameIDMessageSentHandler, cbParams)
	injectCallback(mp, 0, 0, cbFrame)

	// Give the callback time to be processed without panic.
	time.Sleep(200 * time.Millisecond)
}
