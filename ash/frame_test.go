package ash

import (
	"bytes"
	"testing"
)

func TestCRCCCITT(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want uint16
	}{
		{
			name: "RST frame control byte",
			data: []byte{frameTypeRST},
			want: 0x38BC, // known from bellows test vector
		},
		{
			name: "empty",
			data: []byte{},
			want: 0xFFFF, // init value with no data
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := crcCCITT(tt.data)
			if got != tt.want {
				t.Errorf("crcCCITT(%x) = 0x%04X, want 0x%04X", tt.data, got, tt.want)
			}
		})
	}
}

func TestLFSRSequence(t *testing.T) {
	// Known first 10 bytes from bellows/UG101.
	want := []byte{0x42, 0x21, 0xA8, 0x54, 0x2A, 0x15, 0xB2, 0x59, 0x94, 0x4A}

	lfsr := byte(lfsrSeed)
	for i, w := range want {
		if lfsr != w {
			t.Errorf("LFSR[%d] = 0x%02X, want 0x%02X", i, lfsr, w)
		}
		lfsr = nextLFSR(lfsr)
	}
}

func TestStuff(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want []byte
	}{
		{
			name: "no reserved bytes",
			in:   []byte{0x01, 0x02, 0x03},
			want: []byte{0x01, 0x02, 0x03},
		},
		{
			name: "flag byte",
			in:   []byte{0x7E},
			want: []byte{0x7D, 0x5E},
		},
		{
			name: "escape byte",
			in:   []byte{0x7D},
			want: []byte{0x7D, 0x5D},
		},
		{
			name: "XON",
			in:   []byte{0x11},
			want: []byte{0x7D, 0x31},
		},
		{
			name: "XOFF",
			in:   []byte{0x13},
			want: []byte{0x7D, 0x33},
		},
		{
			name: "cancel",
			in:   []byte{0x18},
			want: []byte{0x7D, 0x38},
		},
		{
			name: "substitute",
			in:   []byte{0x1A},
			want: []byte{0x7D, 0x3A},
		},
		{
			name: "mixed",
			in:   []byte{0x01, 0x7E, 0x02, 0x7D, 0x03},
			want: []byte{0x01, 0x7D, 0x5E, 0x02, 0x7D, 0x5D, 0x03},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stuff(tt.in)
			if !bytes.Equal(got, tt.want) {
				t.Errorf("stuff(%x) = %x, want %x", tt.in, got, tt.want)
			}
		})
	}
}

func TestUnstuff(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want []byte
	}{
		{
			name: "no escapes",
			in:   []byte{0x01, 0x02, 0x03},
			want: []byte{0x01, 0x02, 0x03},
		},
		{
			name: "escaped flag",
			in:   []byte{0x7D, 0x5E},
			want: []byte{0x7E},
		},
		{
			name: "escaped escape",
			in:   []byte{0x7D, 0x5D},
			want: []byte{0x7D},
		},
		{
			name: "mixed",
			in:   []byte{0x01, 0x7D, 0x5E, 0x02, 0x7D, 0x5D, 0x03},
			want: []byte{0x01, 0x7E, 0x02, 0x7D, 0x03},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Make a copy since unstuff modifies in-place.
			input := make([]byte, len(tt.in))
			copy(input, tt.in)

			got := unstuff(input)
			if !bytes.Equal(got, tt.want) {
				t.Errorf("unstuff(%x) = %x, want %x", tt.in, got, tt.want)
			}
		})
	}
}

func TestStuffUnstuffRoundtrip(t *testing.T) {
	// All possible byte values should roundtrip.
	original := make([]byte, 256)
	for i := range original {
		original[i] = byte(i)
	}

	stuffed := stuff(original)
	input := make([]byte, len(stuffed))
	copy(input, stuffed)
	unstuffed := unstuff(input)

	if !bytes.Equal(unstuffed, original) {
		t.Errorf("roundtrip failed: got %d bytes, want %d", len(unstuffed), len(original))
	}
}

func TestRandomize(t *testing.T) {
	// Randomize with known data and verify against LFSR sequence.
	data := []byte{0x00, 0x00, 0x00, 0x00}
	randomize(data)

	// XOR with zero should produce the LFSR sequence itself.
	want := []byte{0x42, 0x21, 0xA8, 0x54}
	if !bytes.Equal(data, want) {
		t.Errorf("randomize(zeros) = %x, want %x", data, want)
	}
}

func TestRandomizeDeRandomize(t *testing.T) {
	// Applying randomize twice should restore original data.
	original := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	data := make([]byte, len(original))
	copy(data, original)

	randomize(data)
	randomize(data) // XOR is self-inverse

	if !bytes.Equal(data, original) {
		t.Errorf("double randomize = %x, want %x", data, original)
	}
}

func TestEncodeRST(t *testing.T) {
	got := encodeRST()

	// RST frame: [0xC0] [CRC=0x38BC] [0x7E]
	// 0x38 and 0xBC are not reserved, so no stuffing needed.
	want := []byte{0xC0, 0x38, 0xBC, 0x7E}
	if !bytes.Equal(got, want) {
		t.Errorf("encodeRST() = %x, want %x", got, want)
	}
}

func TestFrameType(t *testing.T) {
	tests := []struct {
		control byte
		want    byte
	}{
		{0x00, frameTypeDATA},
		{0x25, frameTypeDATA},
		{0x7F, frameTypeDATA},
		{0x80, frameTypeACK},
		{0x8F, frameTypeACK},
		{0xA0, frameTypeNAK},
		{0xBF, frameTypeNAK},
		{0xC0, frameTypeRST},
		{0xC1, frameTypeRSTACK},
		{0xC2, frameTypeERROR},
	}
	for _, tt := range tests {
		got := frameType(tt.control)
		if got != tt.want {
			t.Errorf("frameType(0x%02X) = 0x%02X, want 0x%02X", tt.control, got, tt.want)
		}
	}
}

func TestDecodeFrame(t *testing.T) {
	t.Run("RST frame", func(t *testing.T) {
		// RST: [0xC0] [0x38] [0xBC]
		raw := []byte{0xC0, 0x38, 0xBC}
		control, data, err := decodeFrame(raw)
		if err != nil {
			t.Fatalf("decodeFrame() error = %v", err)
		}
		if control != frameTypeRST {
			t.Errorf("control = 0x%02X, want 0x%02X", control, frameTypeRST)
		}
		if len(data) != 0 {
			t.Errorf("data = %x, want empty", data)
		}
	})

	t.Run("CRC mismatch", func(t *testing.T) {
		raw := []byte{0xC0, 0x00, 0x00}
		_, _, err := decodeFrame(raw)
		if err != ErrCRC {
			t.Errorf("decodeFrame() error = %v, want %v", err, ErrCRC)
		}
	})

	t.Run("too short", func(t *testing.T) {
		raw := []byte{0xC0, 0x38}
		_, _, err := decodeFrame(raw)
		if err == nil {
			t.Error("decodeFrame() expected error for short frame")
		}
	})
}

func TestDataControlByte(t *testing.T) {
	tests := []struct {
		name           string
		frmNum, ackNum byte
		reTx           bool
		want           byte
	}{
		{"frmNum=0 ackNum=0", 0, 0, false, 0x00},
		{"frmNum=1 ackNum=0", 1, 0, false, 0x01},
		{"frmNum=0 ackNum=1", 0, 1, false, 0x10},
		{"frmNum=3 ackNum=5", 3, 5, false, 0x53},
		{"retransmission", 2, 3, true, 0x3A},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dataControlByte(tt.frmNum, tt.ackNum, tt.reTx)
			if got != tt.want {
				t.Errorf("dataControlByte(%d, %d, %v) = 0x%02X, want 0x%02X",
					tt.frmNum, tt.ackNum, tt.reTx, got, tt.want)
			}
		})
	}
}

func TestParseDataControl(t *testing.T) {
	frmNum, ackNum, reTx := parseDataControl(0x3A)
	if frmNum != 2 {
		t.Errorf("frmNum = %d, want 2", frmNum)
	}
	if ackNum != 3 {
		t.Errorf("ackNum = %d, want 3", ackNum)
	}
	if !reTx {
		t.Error("reTx = false, want true")
	}
}

func TestParseACKControl(t *testing.T) {
	ackNum, nRdy := parseACKControl(0x81)
	if ackNum != 1 {
		t.Errorf("ackNum = %d, want 1", ackNum)
	}
	if nRdy {
		t.Error("nRdy = true, want false")
	}

	ackNum, nRdy = parseACKControl(0x8B)
	if ackNum != 3 {
		t.Errorf("ackNum = %d, want 3", ackNum)
	}
	if !nRdy {
		t.Error("nRdy = false, want true")
	}
}

func TestCancelBytes(t *testing.T) {
	got := cancelBytes(5)
	want := []byte{0x1A, 0x1A, 0x1A, 0x1A, 0x1A}
	if !bytes.Equal(got, want) {
		t.Errorf("cancelBytes(5) = %x, want %x", got, want)
	}
}

func TestIsReserved(t *testing.T) {
	reserved := []byte{0x7E, 0x7D, 0x11, 0x13, 0x18, 0x1A}
	for _, b := range reserved {
		if !isReserved(b) {
			t.Errorf("isReserved(0x%02X) = false, want true", b)
		}
	}

	notReserved := []byte{0x00, 0x01, 0x10, 0x12, 0x14, 0x19, 0x1B, 0x7C, 0x7F, 0xFF}
	for _, b := range notReserved {
		if isReserved(b) {
			t.Errorf("isReserved(0x%02X) = true, want false", b)
		}
	}
}
