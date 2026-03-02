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
	// Per UG101: bits 6-4 = frmNum, bit 3 = reTx, bits 2-0 = ackNum.
	tests := []struct {
		name           string
		frmNum, ackNum byte
		reTx           bool
		want           byte
	}{
		{"frmNum=0 ackNum=0", 0, 0, false, 0x00},
		{"frmNum=1 ackNum=0", 1, 0, false, 0x10},
		{"frmNum=0 ackNum=1", 0, 1, false, 0x01},
		{"frmNum=3 ackNum=5", 3, 5, false, 0x35},
		{"retransmission", 2, 3, true, 0x2B},
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
	// 0x2B = 0010_1011: frmNum=2 (bits 6-4), reTx=1 (bit 3), ackNum=3 (bits 2-0)
	frmNum, ackNum, reTx := parseDataControl(0x2B)
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

func TestEncodeDecodeDataFrameRoundtrip(t *testing.T) {
	tests := []struct {
		name    string
		control byte
		payload []byte
	}{
		{
			name:    "EZSP version command",
			control: dataControlByte(0, 0, false),
			payload: []byte{0x00, 0x00, 0x00, 0x04},
		},
		{
			name:    "retransmission",
			control: dataControlByte(2, 3, true),
			payload: []byte{0xDE, 0xAD, 0xBE, 0xEF},
		},
		{
			name:    "single byte payload",
			control: dataControlByte(1, 0, false),
			payload: []byte{0xFF},
		},
		{
			name:    "empty payload",
			control: dataControlByte(0, 0, false),
			payload: []byte{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Encode
			wire := encodeDataFrame(tt.control, tt.payload)

			// Simulate receive: strip flag byte, unstuff (no de-randomization in reader)
			if wire[len(wire)-1] != byteFlag {
				t.Fatal("encoded frame missing flag byte")
			}
			raw := make([]byte, len(wire)-1)
			copy(raw, wire[:len(wire)-1])
			raw = unstuff(raw)

			// Decode (handles CRC check + de-randomization)
			control, data, err := decodeFrame(raw)
			if err != nil {
				t.Fatalf("decodeFrame() error = %v", err)
			}
			if control != tt.control {
				t.Errorf("control = 0x%02X, want 0x%02X", control, tt.control)
			}
			if !bytes.Equal(data, tt.payload) {
				t.Errorf("data = %x, want %x", data, tt.payload)
			}
		})
	}
}

func TestEncodeDataFrameRandomizesBeforeCRC(t *testing.T) {
	// Verify that the data field on the wire is randomized but the CRC is not.
	// The CRC should cover [control + randomized data].
	payload := []byte{0x00, 0x00, 0x00, 0x04}
	control := dataControlByte(0, 0, false) // 0x00

	wire := encodeDataFrame(control, payload)

	// Strip flag, unstuff
	raw := make([]byte, len(wire)-1)
	copy(raw, wire[:len(wire)-1])
	raw = unstuff(raw)

	// raw = [control, rand_data..., CRC_hi, CRC_lo]
	// The data portion should be randomized (XOR with LFSR)
	randData := raw[1 : len(raw)-2]
	wantRand := make([]byte, len(payload))
	copy(wantRand, payload)
	randomize(wantRand)

	if !bytes.Equal(randData, wantRand) {
		t.Errorf("on-wire data = %x, want randomized %x", randData, wantRand)
	}

	// The CRC should cover [control + randomized data]
	crcPayload := raw[:len(raw)-2]
	wantCRC := crcCCITT(crcPayload)
	gotCRC := uint16(raw[len(raw)-2])<<8 | uint16(raw[len(raw)-1])
	if gotCRC != wantCRC {
		t.Errorf("CRC = 0x%04X, want 0x%04X", gotCRC, wantCRC)
	}
}

// TestEncodeDataFrameAgainstBellowsReference verifies that encodeDataFrame
// produces identical wire bytes to an independent reference implementation
// derived from bellows (zigpy's ASH layer). The reference uses NO ash package
// functions — it inlines CRC, LFSR, and stuffing from first principles.
//
// This catches regressions in randomization order, CRC scope, control byte
// layout, and byte stuffing that would otherwise only surface on real hardware.
func TestEncodeDataFrameAgainstBellowsReference(t *testing.T) {
	// --- Independent reference implementation (bellows-compatible) ---

	// CRC-CCITT (polynomial 0x1021, init 0xFFFF) — inline table.
	var refCRCTable [256]uint16
	for i := range 256 {
		crc := uint16(i) << 8
		for range 8 {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ 0x1021
			} else {
				crc <<= 1
			}
		}
		refCRCTable[i] = crc
	}
	refCRC := func(data []byte) uint16 {
		crc := uint16(0xFFFF)
		for _, b := range data {
			crc = (crc << 8) ^ refCRCTable[byte(crc>>8)^b]
		}
		return crc
	}

	// LFSR randomization (seed 0x42, polynomial 0xB8).
	refRandomize := func(data []byte) {
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

	// Byte stuffing: escape reserved bytes with [0x7D, b^0x20].
	refIsReserved := func(b byte) bool {
		return b == 0x7E || b == 0x7D || b == 0x11 || b == 0x13 || b == 0x18 || b == 0x1A
	}
	refStuff := func(data []byte) []byte {
		out := make([]byte, 0, len(data)*2)
		for _, b := range data {
			if refIsReserved(b) {
				out = append(out, 0x7D, b^0x20)
			} else {
				out = append(out, b)
			}
		}
		return out
	}

	// Bellows-compatible DATA frame encoder:
	// 1. Randomize payload (NOT control byte)
	// 2. CRC over [control + randomized payload]
	// 3. Stuff [control + randomized payload + CRC_hi + CRC_lo]
	// 4. Append flag byte 0x7E
	refEncode := func(control byte, payload []byte) []byte {
		randData := make([]byte, len(payload))
		copy(randData, payload)
		refRandomize(randData)

		frame := make([]byte, 0, 1+len(payload)+2)
		frame = append(frame, control)
		frame = append(frame, randData...)

		crc := refCRC(frame)
		frame = append(frame, byte(crc>>8), byte(crc))

		frame = refStuff(frame)
		frame = append(frame, 0x7E)
		return frame
	}

	// --- Test vectors ---
	tests := []struct {
		name    string
		control byte
		payload []byte
	}{
		{
			name:    "phase1 EZSP version (frmNum=0 ackNum=0)",
			control: 0x00, // dataControlByte(0, 0, false)
			payload: []byte{0x00, 0x00, 0x00, 0x04},
		},
		{
			name:    "phase2 EZSP extended version (frmNum=1 ackNum=1)",
			control: 0x11, // dataControlByte(1, 1, false)
			payload: []byte{0x01, 0x00, 0x01, 0x00, 0x00, 0x0D},
		},
		{
			name:    "retransmission (frmNum=2 ackNum=3 reTx)",
			control: 0x2B, // dataControlByte(2, 3, true)
			payload: []byte{0xDE, 0xAD, 0xBE, 0xEF},
		},
		{
			name:    "all-zeros payload",
			control: 0x00,
			payload: []byte{0x00, 0x00, 0x00, 0x00},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := refEncode(tt.control, tt.payload)
			got := encodeDataFrame(tt.control, tt.payload)

			if !bytes.Equal(got, want) {
				t.Errorf("encodeDataFrame(0x%02X, %x)\n  got:  %x\n  want: %x",
					tt.control, tt.payload, got, want)
			}
		})
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
