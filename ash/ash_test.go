package ash

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"
)

// mockPort implements serial.Port for testing.
type mockPort struct {
	mu       sync.Mutex
	readBuf  *bytes.Buffer
	writeBuf *bytes.Buffer
	flushed  bool
	closed   bool

	// onWrite is called after each Write, allowing tests to inject responses.
	onWrite func(data []byte)
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

	// If no data available, return 0 to simulate VTIME timeout.
	if m.readBuf.Len() == 0 {
		// Simulate VTIME=1 (100ms) timeout to allow reader goroutine to check done.
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
	m.flushed = true
	m.readBuf.Reset()
	return nil
}

// injectFrame writes raw frame bytes into the mock port's read buffer,
// simulating data arriving from the NCP.
func (m *mockPort) injectFrame(data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.readBuf.Write(data)
}

// buildRSTACKFrame builds a raw RSTACK response as it would appear on the wire.
func buildRSTACKFrame(version, resetCode byte) []byte {
	// RSTACK: [0xC1] [version] [resetCode] [CRC-hi] [CRC-lo]
	raw := []byte{frameTypeRSTACK, version, resetCode}
	crc := crcCCITT(raw)
	raw = append(raw, byte(crc>>8), byte(crc))
	// Byte-stuff and append flag.
	raw = stuff(raw)
	raw = append(raw, byteFlag)
	return raw
}

func TestResetHandshake(t *testing.T) {
	mp := newMockPort()

	// When the host writes (RST), inject a RSTACK response.
	rstackSent := false
	mp.onWrite = func(data []byte) {
		if rstackSent {
			return
		}
		// Check if data contains the RST frame flag byte.
		if bytes.Contains(data, []byte{byteFlag}) {
			rstackSent = true
			// Inject RSTACK after a small delay to simulate NCP response time.
			go func() {
				time.Sleep(5 * time.Millisecond)
				mp.injectFrame(buildRSTACKFrame(0x0B, 0x01))
			}()
		}
	}

	conn := New(mp)
	t.Cleanup(func() {
		conn.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	version, resetCode, err := conn.Reset(ctx)
	if err != nil {
		t.Fatalf("Reset() error = %v", err)
	}
	if version != 0x0B {
		t.Errorf("version = 0x%02X, want 0x0B", version)
	}
	if resetCode != 0x01 {
		t.Errorf("resetCode = 0x%02X, want 0x01", resetCode)
	}

	// Verify CANCEL bytes were sent.
	written := mp.writeBuf.Bytes()
	cancelPrefix := cancelBytes(cancelCount)
	if !bytes.HasPrefix(written, cancelPrefix) {
		t.Errorf("expected %d CANCEL bytes prefix, got %x", cancelCount, written[:min(len(written), cancelCount)])
	}
}

func TestResetTimeout(t *testing.T) {
	mp := newMockPort()
	// Don't inject any response — should timeout.

	conn := New(mp)
	t.Cleanup(func() {
		conn.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, _, err := conn.Reset(ctx)
	if err == nil {
		t.Fatal("Reset() expected error on timeout")
	}
}

func TestResetContextCancelled(t *testing.T) {
	mp := newMockPort()

	conn := New(mp)
	t.Cleanup(func() {
		conn.Close()
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	_, _, err := conn.Reset(ctx)
	if err == nil {
		t.Fatal("Reset() expected error on cancelled context")
	}
}

func TestRecvDataFrame(t *testing.T) {
	mp := newMockPort()
	conn := New(mp)
	t.Cleanup(func() { conn.Close() })

	// Inject a DATA frame with frmNum=0, ackNum=0, payload = [0xAA, 0xBB].
	control := dataControlByte(0, 0, false)
	frame := encodeDataFrame(control, []byte{0xAA, 0xBB})
	go func() {
		time.Sleep(10 * time.Millisecond)
		mp.injectFrame(frame)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	payload, err := conn.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv() error = %v", err)
	}
	if !bytes.Equal(payload, []byte{0xAA, 0xBB}) {
		t.Errorf("payload = %x, want aabb", payload)
	}

	// Verify ACK was sent (ackNum should be 1 = (frmNum+1)&0x07).
	if conn.ackNum != 1 {
		t.Errorf("ackNum = %d, want 1", conn.ackNum)
	}

	// Verify frmNum was NOT advanced.
	if conn.frmNum != 0 {
		t.Errorf("frmNum = %d, want 0 (should not be advanced by Recv)", conn.frmNum)
	}

	// Verify ACK frame was written.
	written := mp.writeBuf.Bytes()
	if len(written) == 0 {
		t.Fatal("expected ACK frame to be written")
	}
}

func TestRecvSkipsACK(t *testing.T) {
	mp := newMockPort()
	conn := New(mp)
	t.Cleanup(func() { conn.Close() })

	// Inject an ACK frame followed by a DATA frame.
	// The ACK should be skipped, and Recv should return the DATA payload.
	ackFrame := encodeACK(0)
	control := dataControlByte(0, 0, false)
	dataFrame := encodeDataFrame(control, []byte{0xCC})

	go func() {
		time.Sleep(10 * time.Millisecond)
		mp.injectFrame(ackFrame)
		time.Sleep(10 * time.Millisecond)
		mp.injectFrame(dataFrame)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	payload, err := conn.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv() error = %v", err)
	}
	if !bytes.Equal(payload, []byte{0xCC}) {
		t.Errorf("payload = %x, want cc", payload)
	}
}

func TestRecvRSTACK(t *testing.T) {
	mp := newMockPort()
	conn := New(mp)
	t.Cleanup(func() { conn.Close() })

	// Inject a RSTACK frame — Recv should return ErrConnectionReset.
	go func() {
		time.Sleep(10 * time.Millisecond)
		mp.injectFrame(buildRSTACKFrame(0x0B, 0x01))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := conn.Recv(ctx)
	if err != ErrConnectionReset {
		t.Fatalf("Recv() error = %v, want ErrConnectionReset", err)
	}
}

func TestRecvContextCancelled(t *testing.T) {
	mp := newMockPort()
	conn := New(mp)
	t.Cleanup(func() { conn.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	_, err := conn.Recv(ctx)
	if err == nil {
		t.Fatal("Recv() expected error on cancelled context")
	}
	if err != context.Canceled {
		t.Fatalf("Recv() error = %v, want context.Canceled", err)
	}
}

func TestRecvReaderClosed(t *testing.T) {
	mp := newMockPort()
	conn := New(mp)

	// Close the connection to close the frames channel.
	conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := conn.Recv(ctx)
	if err == nil {
		t.Fatal("Recv() expected error when reader is closed")
	}
}

func TestRecvMultipleDataFrames(t *testing.T) {
	mp := newMockPort()
	conn := New(mp)
	t.Cleanup(func() { conn.Close() })

	// Simulate a scan scenario: inject 3 consecutive DATA frames.
	// Each has increasing frmNum.
	go func() {
		time.Sleep(10 * time.Millisecond)
		for i := range 3 {
			control := dataControlByte(byte(i), 0, false)
			frame := encodeDataFrame(control, []byte{byte(0x10 + i)})
			mp.injectFrame(frame)
			time.Sleep(10 * time.Millisecond)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	for i := range 3 {
		payload, err := conn.Recv(ctx)
		if err != nil {
			t.Fatalf("Recv() #%d error = %v", i, err)
		}
		want := []byte{byte(0x10 + i)}
		if !bytes.Equal(payload, want) {
			t.Errorf("Recv() #%d = %x, want %x", i, payload, want)
		}
	}

	// After 3 frames with frmNum 0,1,2, ackNum should be 3.
	if conn.ackNum != 3 {
		t.Errorf("ackNum = %d, want 3", conn.ackNum)
	}

	// frmNum should still be 0 — Recv never advances it.
	if conn.frmNum != 0 {
		t.Errorf("frmNum = %d, want 0", conn.frmNum)
	}
}

func TestConnClose(t *testing.T) {
	mp := newMockPort()
	conn := New(mp)

	// Close should not hang.
	done := make(chan struct{})
	go func() {
		conn.Close()
		close(done)
	}()

	select {
	case <-done:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("Close() timed out — reader goroutine may have hung")
	}
}
