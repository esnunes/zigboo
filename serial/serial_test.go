package serial

import (
	"bytes"
	"io"
	"testing"
)

// mockPort implements Port for testing.
type mockPort struct {
	io.ReadWriter
	flushed bool
	closed  bool
}

func newMockPort(data []byte) *mockPort {
	return &mockPort{ReadWriter: bytes.NewBuffer(data)}
}

func (m *mockPort) Close() error {
	m.closed = true
	return nil
}

func (m *mockPort) Flush() error {
	m.flushed = true
	return nil
}

func TestMockPortImplementsPort(t *testing.T) {
	var _ Port = (*mockPort)(nil)
}

func TestMockPortRead(t *testing.T) {
	want := []byte{0x01, 0x02, 0x03}
	p := newMockPort(want)

	got := make([]byte, 3)
	n, err := p.Read(got)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if n != 3 {
		t.Fatalf("Read() n = %d, want 3", n)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Read() = %x, want %x", got, want)
	}
}

func TestMockPortWrite(t *testing.T) {
	p := newMockPort(nil)

	data := []byte{0x04, 0x05}
	n, err := p.Write(data)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if n != 2 {
		t.Fatalf("Write() n = %d, want 2", n)
	}
}

func TestMockPortFlush(t *testing.T) {
	p := newMockPort(nil)

	if err := p.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if !p.flushed {
		t.Fatal("Flush() did not set flushed flag")
	}
}

func TestMockPortClose(t *testing.T) {
	p := newMockPort(nil)

	if err := p.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !p.closed {
		t.Fatal("Close() did not set closed flag")
	}
}

func TestConfigSetDefaults(t *testing.T) {
	t.Run("zero value gets defaults", func(t *testing.T) {
		cfg := Config{}
		cfg.setDefaults()

		if cfg.BaudRate != 115200 {
			t.Fatalf("BaudRate = %d, want 115200", cfg.BaudRate)
		}
	})

	t.Run("explicit value preserved", func(t *testing.T) {
		cfg := Config{BaudRate: 9600}
		cfg.setDefaults()

		if cfg.BaudRate != 9600 {
			t.Fatalf("BaudRate = %d, want 9600", cfg.BaudRate)
		}
	})
}
