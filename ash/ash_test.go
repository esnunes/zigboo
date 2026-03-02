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
