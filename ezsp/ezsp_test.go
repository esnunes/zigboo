package ezsp

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/esnunes/zigboo/ash"
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
	control := 0x80 | (ackNum & 0x07)
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
// Each EZSP command sent by the host triggers the next response from the queue.
// The response payloads are full EZSP frames (seq + fc + frameID + params).
// Returns the Client ready for use after RST/RSTACK handshake.
func setupMockNCP(t *testing.T, responses [][]byte) (*Client, *ash.Conn, *mockPort) {
	t.Helper()
	mp := newMockPort()

	var (
		mu         sync.Mutex
		handshook  bool
		respIdx    int
		ncpFrmNum  byte
		hostFrmNum byte // track host's expected frame number for ACK
	)

	mp.onWrite = func(data []byte) {
		mu.Lock()
		defer mu.Unlock()

		if !handshook {
			// First write with flag byte is RST — respond with RSTACK.
			if bytes.Contains(data, []byte{0x7E}) {
				handshook = true
				go func() {
					time.Sleep(5 * time.Millisecond)
					mp.injectFrame(testEncodeRSTACK(0x0B, 0x01))
				}()
			}
			return
		}

		// After handshake, respond to DATA frames (contain 0x7E flag).
		if !bytes.Contains(data, []byte{0x7E}) {
			return
		}

		// Check if this is an ACK (short frame, no response needed).
		// ACK frames are ~4 bytes on wire. DATA frames are longer.
		if len(data) < 8 {
			return
		}

		go func() {
			time.Sleep(5 * time.Millisecond)

			mu.Lock()
			// Send ACK for host's frame.
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

	client := New(conn)
	client.extended = true
	client.version = 13

	t.Cleanup(func() {
		conn.Close()
	})

	return client, conn, mp
}

// --- Existing tests ---

func TestVersionInfoStackVersionString(t *testing.T) {
	tests := []struct {
		name         string
		stackVersion uint16
		want         string
	}{
		{"7.4.0", 0x7400, "7.4.0"},
		{"8.0.1", 0x8001, "8.0.1"},
		{"13.2.208", 0xD2D0, "13.2.208"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := VersionInfo{StackVersion: tt.stackVersion}
			got := v.StackVersionString()
			if got != tt.want {
				t.Errorf("StackVersionString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseVersionResponse(t *testing.T) {
	t.Run("legacy", func(t *testing.T) {
		// Legacy response: seq=0, fc=0x80, frameID=0x00, protocolVersion=13, stackType=2, stackVersion=0x07D0
		data := []byte{0x00, 0x80, 0x00, 0x0D, 0x02, 0xD0, 0x07}
		info, err := parseVersionResponse(data, false)
		if err != nil {
			t.Fatalf("parseVersionResponse() error = %v", err)
		}
		if info.ProtocolVersion != 13 {
			t.Errorf("ProtocolVersion = %d, want 13", info.ProtocolVersion)
		}
		if info.StackType != 2 {
			t.Errorf("StackType = %d, want 2", info.StackType)
		}
		if info.StackVersion != 0x07D0 {
			t.Errorf("StackVersion = 0x%04X, want 0x07D0", info.StackVersion)
		}
	})

	t.Run("extended", func(t *testing.T) {
		// Extended response: seq=1, fc_lo=0x80, fc_hi=0x01, frameID=0x0000, protocolVersion=13, stackType=2, stackVersion=0x07D0
		data := []byte{0x01, 0x80, 0x01, 0x00, 0x00, 0x0D, 0x02, 0xD0, 0x07}
		info, err := parseVersionResponse(data, true)
		if err != nil {
			t.Fatalf("parseVersionResponse() error = %v", err)
		}
		if info.ProtocolVersion != 13 {
			t.Errorf("ProtocolVersion = %d, want 13", info.ProtocolVersion)
		}
		if info.StackType != 2 {
			t.Errorf("StackType = %d, want 2", info.StackType)
		}
		if info.StackVersion != 0x07D0 {
			t.Errorf("StackVersion = 0x%04X, want 0x07D0", info.StackVersion)
		}
	})

	t.Run("legacy too short", func(t *testing.T) {
		data := []byte{0x00, 0x00, 0x00, 0x0D}
		_, err := parseVersionResponse(data, false)
		if err == nil {
			t.Fatal("expected error for short response")
		}
	})
}

func TestFormatEUI64(t *testing.T) {
	// EUI-64 is stored little-endian; display should be big-endian (MSB first).
	eui := [8]byte{0x14, 0x73, 0x89, 0x25, 0x00, 0x4B, 0x12, 0x00}
	got := FormatEUI64(eui)
	want := "00:12:4B:00:25:89:73:14"
	if got != want {
		t.Errorf("FormatEUI64() = %q, want %q", got, want)
	}
}

// --- NetworkState tests ---

func TestNetworkState(t *testing.T) {
	tests := []struct {
		name   string
		status EmberNetworkStatus
	}{
		{"no network", NetworkStatusNoNetwork},
		{"joining", NetworkStatusJoiningNetwork},
		{"joined", NetworkStatusJoinedNetwork},
		{"joined no parent", NetworkStatusJoinedNoParent},
		{"leaving", NetworkStatusLeavingNetwork},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build extended EZSP response: seq=0, fc_lo=0x00, fc_hi=0x01, frameID=0x0018, params=[status]
			resp := encodeExtended(0, frameIDNetworkState, []byte{byte(tt.status)})
			client, _, _ := setupMockNCP(t, [][]byte{resp})

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			got, err := client.NetworkState(ctx)
			if err != nil {
				t.Fatalf("NetworkState() error = %v", err)
			}
			if got != tt.status {
				t.Errorf("NetworkState() = %d, want %d", got, tt.status)
			}
		})
	}
}

func TestNetworkState_ResponseTooShort(t *testing.T) {
	// Response with no params.
	resp := encodeExtended(0, frameIDNetworkState, nil)
	client, _, _ := setupMockNCP(t, [][]byte{resp})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := client.NetworkState(ctx)
	if err == nil {
		t.Fatal("expected error for short response")
	}
}

// --- GetNetworkParameters tests ---

func TestGetNetworkParameters(t *testing.T) {
	// Build response: EmberStatus=0x00, NodeType=0x01(coordinator),
	// ExtendedPanID=[0x01..0x08], PanID=0x1A2B, TxPower=8, Channel=15
	params := []byte{
		0x00,                                           // EmberStatus success
		0x01,                                           // NodeType coordinator
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, // ExtendedPanID
		0x2B, 0x1A, // PanID 0x1A2B (little-endian)
		0x08, // TxPower 8
		0x0F, // Channel 15
	}
	resp := encodeExtended(0, frameIDGetNetworkParameters, params)
	client, _, _ := setupMockNCP(t, [][]byte{resp})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	nodeType, netParams, err := client.GetNetworkParameters(ctx)
	if err != nil {
		t.Fatalf("GetNetworkParameters() error = %v", err)
	}
	if nodeType != NodeTypeCoordinator {
		t.Errorf("nodeType = %d, want %d (coordinator)", nodeType, NodeTypeCoordinator)
	}
	wantExtPan := [8]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	if netParams.ExtendedPanID != wantExtPan {
		t.Errorf("ExtendedPanID = %x, want %x", netParams.ExtendedPanID, wantExtPan)
	}
	if netParams.PanID != 0x1A2B {
		t.Errorf("PanID = 0x%04X, want 0x1A2B", netParams.PanID)
	}
	if netParams.RadioTxPower != 8 {
		t.Errorf("RadioTxPower = %d, want 8", netParams.RadioTxPower)
	}
	if netParams.RadioChannel != 15 {
		t.Errorf("RadioChannel = %d, want 15", netParams.RadioChannel)
	}
}

func TestGetNetworkParameters_EmberStatusError(t *testing.T) {
	// EmberStatus = 0x70 (not success), rest is garbage.
	params := make([]byte, 14)
	params[0] = 0x70
	resp := encodeExtended(0, frameIDGetNetworkParameters, params)
	client, _, _ := setupMockNCP(t, [][]byte{resp})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, _, err := client.GetNetworkParameters(ctx)
	if err == nil {
		t.Fatal("expected error for non-zero EmberStatus")
	}
}

func TestGetNetworkParameters_ResponseTooShort(t *testing.T) {
	// Only 5 bytes — too short for the full response.
	params := []byte{0x00, 0x01, 0x02, 0x03, 0x04}
	resp := encodeExtended(0, frameIDGetNetworkParameters, params)
	client, _, _ := setupMockNCP(t, [][]byte{resp})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, _, err := client.GetNetworkParameters(ctx)
	if err == nil {
		t.Fatal("expected error for short response")
	}
}

// --- Scan tests ---

// injectCallbacks injects a sequence of EZSP callback frames into the mock port
// as unsolicited NCP DATA frames. Each callback is an EZSP frame payload.
// The frames are injected with small delays to simulate real NCP timing.
func injectCallbacks(mp *mockPort, callbacks [][]byte, startFrmNum byte) {
	frmNum := startFrmNum
	for _, cb := range callbacks {
		time.Sleep(15 * time.Millisecond)
		control := testDataControl(frmNum, 0)
		mp.injectFrame(testEncodeDataFrame(control, cb))
		frmNum = (frmNum + 1) & 0x07
	}
}

func TestStartEnergyScan(t *testing.T) {
	// startScan response: EmberStatus=0x00 (success)
	scanResp := encodeExtended(0, frameIDStartScan, []byte{0x00})

	// Callback frames: 3 energy results + scanComplete
	cb1 := encodeExtended(0, frameIDEnergyScanResultHandler, []byte{11, 0xA9}) // ch11, RSSI=-87
	cb2 := encodeExtended(0, frameIDEnergyScanResultHandler, []byte{12, 0xA4}) // ch12, RSSI=-92
	cb3 := encodeExtended(0, frameIDEnergyScanResultHandler, []byte{13, 0xA1}) // ch13, RSSI=-95
	cbDone := encodeExtended(0, frameIDScanCompleteHandler, []byte{13, 0x00})  // ch13, status=success

	client, _, mp := setupMockNCP(t, [][]byte{scanResp})

	// After the startScan response is handled, inject callbacks.
	// The NCP frmNum starts at 1 because frmNum=0 was used for the startScan response.
	go func() {
		time.Sleep(100 * time.Millisecond) // wait for startScan command to complete
		injectCallbacks(mp, [][]byte{cb1, cb2, cb3, cbDone}, 1)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	results, errCh, err := client.StartEnergyScan(ctx, DefaultChannelMask, 3)
	if err != nil {
		t.Fatalf("StartEnergyScan() error = %v", err)
	}

	var got []EnergyScanResult
	for r := range results {
		got = append(got, r)
	}
	if scanErr := <-errCh; scanErr != nil {
		t.Fatalf("scan error = %v", scanErr)
	}

	if len(got) != 3 {
		t.Fatalf("got %d results, want 3", len(got))
	}
	if got[0].Channel != 11 || got[0].MaxRSSI != -87 {
		t.Errorf("result[0] = {%d, %d}, want {11, -87}", got[0].Channel, got[0].MaxRSSI)
	}
	if got[1].Channel != 12 || got[1].MaxRSSI != -92 {
		t.Errorf("result[1] = {%d, %d}, want {12, -92}", got[1].Channel, got[1].MaxRSSI)
	}
	if got[2].Channel != 13 || got[2].MaxRSSI != -95 {
		t.Errorf("result[2] = {%d, %d}, want {13, -95}", got[2].Channel, got[2].MaxRSSI)
	}
}

func TestStartEnergyScan_ScanFailed(t *testing.T) {
	// startScan response: EmberStatus=0x70 (failure)
	scanResp := encodeExtended(0, frameIDStartScan, []byte{0x70})
	client, _, _ := setupMockNCP(t, [][]byte{scanResp})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, _, err := client.StartEnergyScan(ctx, DefaultChannelMask, 3)
	if err == nil {
		t.Fatal("expected error for failed scan start")
	}
}

func TestStartEnergyScan_ContextCancelled(t *testing.T) {
	// startScan succeeds but no callbacks arrive — context gets cancelled.
	scanResp := encodeExtended(0, frameIDStartScan, []byte{0x00})
	client, _, _ := setupMockNCP(t, [][]byte{scanResp})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	results, errCh, err := client.StartEnergyScan(ctx, DefaultChannelMask, 3)
	if err != nil {
		t.Fatalf("StartEnergyScan() error = %v", err)
	}

	// Cancel after a short delay.
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	// Drain results — should be empty.
	for range results {
	}

	// Error channel should have the context error.
	scanErr := <-errCh
	if scanErr == nil {
		t.Fatal("expected context error")
	}

	// scanning flag should be cleared.
	client.mu.Lock()
	scanning := client.scanning
	client.mu.Unlock()
	if scanning {
		t.Error("scanning flag should be false after cancel")
	}
}

func TestStartActiveScan(t *testing.T) {
	// startScan response: success
	scanResp := encodeExtended(0, frameIDStartScan, []byte{0x00})

	// Callback: networkFoundHandler with a discovered network.
	// channel=15, panId=0x1A2B, extPanId=[01..08], allowJoin=1, stackProfile=2,
	// nwkUpdateId=3, lqi=255, rssi=-45
	nwkParams := []byte{
		15,                                              // channel
		0x2B, 0x1A,                                      // panId LE
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, // extPanId
		0x01,       // allowingJoin
		0x02,       // stackProfile
		0x03,       // nwkUpdateId
		0xFF,       // lqi
		byte(0xD3), // rssi = -45
	}
	cb1 := encodeExtended(0, frameIDNetworkFoundHandler, nwkParams)
	cbDone := encodeExtended(0, frameIDScanCompleteHandler, []byte{15, 0x00})

	client, _, mp := setupMockNCP(t, [][]byte{scanResp})

	go func() {
		time.Sleep(100 * time.Millisecond)
		injectCallbacks(mp, [][]byte{cb1, cbDone}, 1)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	results, errCh, err := client.StartActiveScan(ctx, DefaultChannelMask, 3)
	if err != nil {
		t.Fatalf("StartActiveScan() error = %v", err)
	}

	var got []NetworkScanResult
	for r := range results {
		got = append(got, r)
	}
	if scanErr := <-errCh; scanErr != nil {
		t.Fatalf("scan error = %v", scanErr)
	}

	if len(got) != 1 {
		t.Fatalf("got %d results, want 1", len(got))
	}
	r := got[0]
	if r.Channel != 15 {
		t.Errorf("Channel = %d, want 15", r.Channel)
	}
	if r.PanID != 0x1A2B {
		t.Errorf("PanID = 0x%04X, want 0x1A2B", r.PanID)
	}
	wantExtPan := [8]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	if r.ExtendedPanID != wantExtPan {
		t.Errorf("ExtendedPanID = %x, want %x", r.ExtendedPanID, wantExtPan)
	}
	if !r.AllowingJoin {
		t.Error("AllowingJoin = false, want true")
	}
	if r.StackProfile != 2 {
		t.Errorf("StackProfile = %d, want 2", r.StackProfile)
	}
	if r.LQI != 255 {
		t.Errorf("LQI = %d, want 255", r.LQI)
	}
	if r.RSSI != -45 {
		t.Errorf("RSSI = %d, want -45", r.RSSI)
	}
}

func TestStartActiveScan_ZeroResults(t *testing.T) {
	scanResp := encodeExtended(0, frameIDStartScan, []byte{0x00})
	cbDone := encodeExtended(0, frameIDScanCompleteHandler, []byte{26, 0x00})

	client, _, mp := setupMockNCP(t, [][]byte{scanResp})

	go func() {
		time.Sleep(100 * time.Millisecond)
		injectCallbacks(mp, [][]byte{cbDone}, 1)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	results, errCh, err := client.StartActiveScan(ctx, DefaultChannelMask, 3)
	if err != nil {
		t.Fatalf("StartActiveScan() error = %v", err)
	}

	count := 0
	for range results {
		count++
	}
	if count != 0 {
		t.Errorf("got %d results, want 0", count)
	}
	if scanErr := <-errCh; scanErr != nil {
		t.Fatalf("scan error = %v", scanErr)
	}
}

func TestStartActiveScan_NoBeaconsStatus(t *testing.T) {
	// EMBER_NO_BEACONS (0x36) is the normal completion status for active scans
	// when the NCP finishes scanning with no more beacons to report.
	scanResp := encodeExtended(0, frameIDStartScan, []byte{0x00})
	cbDone := encodeExtended(0, frameIDScanCompleteHandler, []byte{26, emberNoBeacons})

	client, _, mp := setupMockNCP(t, [][]byte{scanResp})

	go func() {
		time.Sleep(100 * time.Millisecond)
		injectCallbacks(mp, [][]byte{cbDone}, 1)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	results, errCh, err := client.StartActiveScan(ctx, DefaultChannelMask, 3)
	if err != nil {
		t.Fatalf("StartActiveScan() error = %v", err)
	}

	for range results {
	}
	if scanErr := <-errCh; scanErr != nil {
		t.Fatalf("EMBER_NO_BEACONS (0x36) should not be an error, got: %v", scanErr)
	}
}

func TestScanExclusivity(t *testing.T) {
	// Start a scan, verify Command() returns ErrScanInProgress.
	scanResp := encodeExtended(0, frameIDStartScan, []byte{0x00})

	client, _, mp := setupMockNCP(t, [][]byte{scanResp})

	// Inject scanComplete after a delay so the scan finishes eventually.
	cbDone := encodeExtended(0, frameIDScanCompleteHandler, []byte{11, 0x00})
	go func() {
		time.Sleep(200 * time.Millisecond)
		injectCallbacks(mp, [][]byte{cbDone}, 1)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	results, errCh, err := client.StartEnergyScan(ctx, DefaultChannelMask, 3)
	if err != nil {
		t.Fatalf("StartEnergyScan() error = %v", err)
	}

	// While scan is in progress, Command() should return ErrScanInProgress.
	_, cmdErr := client.Command(ctx, frameIDNetworkState, nil)
	if cmdErr != ErrScanInProgress {
		t.Errorf("Command() during scan = %v, want ErrScanInProgress", cmdErr)
	}

	// Drain scan results.
	for range results {
	}
	<-errCh

	// After scan completes, scanning should be false.
	client.mu.Lock()
	scanning := client.scanning
	client.mu.Unlock()
	if scanning {
		t.Error("scanning flag should be false after scan completes")
	}
}

func TestStartEnergyScan_ScanCompleteWithError(t *testing.T) {
	scanResp := encodeExtended(0, frameIDStartScan, []byte{0x00})
	cb1 := encodeExtended(0, frameIDEnergyScanResultHandler, []byte{11, 0xA9})
	// scanComplete with error status 0x35
	cbDone := encodeExtended(0, frameIDScanCompleteHandler, []byte{11, 0x35})

	client, _, mp := setupMockNCP(t, [][]byte{scanResp})

	go func() {
		time.Sleep(100 * time.Millisecond)
		injectCallbacks(mp, [][]byte{cb1, cbDone}, 1)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	results, errCh, err := client.StartEnergyScan(ctx, DefaultChannelMask, 3)
	if err != nil {
		t.Fatalf("StartEnergyScan() error = %v", err)
	}

	// Should still get the one result before the error.
	count := 0
	for range results {
		count++
	}
	if count != 1 {
		t.Errorf("got %d results, want 1", count)
	}

	// Error channel should have the scan complete error.
	scanErr := <-errCh
	if scanErr == nil {
		t.Fatal("expected error from scanComplete with non-zero status")
	}
}

func TestStartEnergyScan_UnexpectedCallback(t *testing.T) {
	scanResp := encodeExtended(0, frameIDStartScan, []byte{0x00})
	// Inject an unexpected callback (stackStatusHandler = 0x0019) between energy results.
	cbUnexpected := encodeExtended(0, 0x0019, []byte{0x02})
	cb1 := encodeExtended(0, frameIDEnergyScanResultHandler, []byte{11, 0xA9})
	cbDone := encodeExtended(0, frameIDScanCompleteHandler, []byte{11, 0x00})

	client, _, mp := setupMockNCP(t, [][]byte{scanResp})

	go func() {
		time.Sleep(100 * time.Millisecond)
		injectCallbacks(mp, [][]byte{cbUnexpected, cb1, cbDone}, 1)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	results, errCh, err := client.StartEnergyScan(ctx, DefaultChannelMask, 3)
	if err != nil {
		t.Fatalf("StartEnergyScan() error = %v", err)
	}

	// Should get 1 energy result (unexpected callback is skipped).
	count := 0
	for range results {
		count++
	}
	if count != 1 {
		t.Errorf("got %d results, want 1 (unexpected callback should be skipped)", count)
	}
	if scanErr := <-errCh; scanErr != nil {
		t.Fatalf("scan error = %v", scanErr)
	}
}
