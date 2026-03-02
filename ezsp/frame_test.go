package ezsp

import (
	"bytes"
	"testing"
)

func TestEncodeLegacy(t *testing.T) {
	tests := []struct {
		name    string
		seq     byte
		frameID uint16
		params  []byte
		want    []byte
	}{
		{
			name:    "version command",
			seq:     0,
			frameID: frameIDVersion,
			params:  []byte{4},
			want:    []byte{0x00, 0x00, 0x00, 0x04},
		},
		{
			name:    "no params",
			seq:     1,
			frameID: frameIDGetNodeID,
			params:  nil,
			want:    []byte{0x01, 0x00, 0x27},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := encodeLegacy(tt.seq, tt.frameID, tt.params)
			if !bytes.Equal(got, tt.want) {
				t.Errorf("encodeLegacy() = %x, want %x", got, tt.want)
			}
		})
	}
}

func TestEncodeExtended(t *testing.T) {
	tests := []struct {
		name    string
		seq     byte
		frameID uint16
		params  []byte
		want    []byte
	}{
		{
			name:    "version command",
			seq:     0,
			frameID: frameIDVersion,
			params:  []byte{13},
			want:    []byte{0x00, 0x00, 0x01, 0x00, 0x00, 0x0D},
		},
		{
			name:    "getNodeId",
			seq:     2,
			frameID: frameIDGetNodeID,
			params:  nil,
			want:    []byte{0x02, 0x00, 0x01, 0x27, 0x00},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := encodeExtended(tt.seq, tt.frameID, tt.params)
			if !bytes.Equal(got, tt.want) {
				t.Errorf("encodeExtended() = %x, want %x", got, tt.want)
			}
		})
	}
}

func TestDecodeLegacy(t *testing.T) {
	// Simulate a version response: seq=0, fc=0x00, frameID=0x00, params=[13, 2, 0xD0, 0x07]
	data := []byte{0x00, 0x00, 0x00, 0x0D, 0x02, 0xD0, 0x07}
	seq, frameID, params, err := decodeLegacy(data)
	if err != nil {
		t.Fatalf("decodeLegacy() error = %v", err)
	}
	if seq != 0 {
		t.Errorf("seq = %d, want 0", seq)
	}
	if frameID != 0 {
		t.Errorf("frameID = 0x%04X, want 0x0000", frameID)
	}
	if !bytes.Equal(params, []byte{0x0D, 0x02, 0xD0, 0x07}) {
		t.Errorf("params = %x, want 0d02d007", params)
	}
}

func TestDecodeExtended(t *testing.T) {
	// Extended version response: seq=1, fc_lo=0x00, fc_hi=0x01, frameID=0x0000, params=[13, 2, 0xD0, 0x07]
	data := []byte{0x01, 0x00, 0x01, 0x00, 0x00, 0x0D, 0x02, 0xD0, 0x07}
	seq, frameID, params, err := decodeExtended(data)
	if err != nil {
		t.Fatalf("decodeExtended() error = %v", err)
	}
	if seq != 1 {
		t.Errorf("seq = %d, want 1", seq)
	}
	if frameID != 0 {
		t.Errorf("frameID = 0x%04X, want 0x0000", frameID)
	}
	if !bytes.Equal(params, []byte{0x0D, 0x02, 0xD0, 0x07}) {
		t.Errorf("params = %x, want 0d02d007", params)
	}
}

func TestDecodeLegacyTooShort(t *testing.T) {
	_, _, _, err := decodeLegacy([]byte{0x00, 0x00})
	if err != ErrFrameTooShort {
		t.Errorf("decodeLegacy() error = %v, want %v", err, ErrFrameTooShort)
	}
}

func TestDecodeExtendedTooShort(t *testing.T) {
	_, _, _, err := decodeExtended([]byte{0x00, 0x00, 0x01, 0x00})
	if err != ErrFrameTooShort {
		t.Errorf("decodeExtended() error = %v, want %v", err, ErrFrameTooShort)
	}
}

func TestIsExtendedFormat(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{"legacy", []byte{0x00, 0x00, 0x00, 0x04}, false},
		{"extended", []byte{0x00, 0x00, 0x01, 0x00, 0x00}, true},
		{"too short", []byte{0x00, 0x00}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isExtendedFormat(tt.data)
			if got != tt.want {
				t.Errorf("isExtendedFormat(%x) = %v, want %v", tt.data, got, tt.want)
			}
		})
	}
}
