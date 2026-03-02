package ezsp

import (
	"testing"
)

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
