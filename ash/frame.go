// Package ash implements the ASH (Asynchronous Serial Host) transport protocol
// for communicating with Silicon Labs Zigbee network co-processors.
//
// ASH provides reliable framing over a serial UART link, including CRC
// verification, byte stuffing, data randomization, and retransmission.
// See UG101 for the full specification.
package ash

import "errors"

// Frame type constants derived from the control byte.
const (
	// Control byte ranges for frame type identification.
	frameTypeDATA   = 0x00 // 0x00-0x7F: DATA frame
	frameTypeACK    = 0x80 // 0x80-0x9F: ACK frame
	frameTypeNAK    = 0xA0 // 0xA0-0xBF: NAK frame
	frameTypeRST    = 0xC0 // RST frame
	frameTypeRSTACK = 0xC1 // RSTACK frame
	frameTypeERROR  = 0xC2 // ERROR frame
)

// Reserved bytes that must be escaped during byte stuffing.
const (
	byteFlag       = 0x7E // Frame delimiter
	byteEscape     = 0x7D // Escape byte
	byteXON        = 0x11 // XON flow control
	byteXOFF       = 0x13 // XOFF flow control
	byteCancel     = 0x18 // Cancel byte
	byteSubstitute = 0x1A // Substitute byte
)

// LFSR constants for data randomization.
const (
	lfsrSeed       = 0x42
	lfsrPolynomial = 0xB8
)

// Maximum frame buffer size (on-wire, pre-unstuff).
const maxFrameSize = 512

// Sentinel errors.
var (
	// ErrTimeout indicates an operation exceeded its deadline.
	ErrTimeout = errors.New("ash: timeout")

	// ErrConnectionReset indicates the NCP sent an unsolicited RSTACK.
	ErrConnectionReset = errors.New("ash: connection reset")

	// ErrCRC indicates a frame failed CRC verification.
	ErrCRC = errors.New("ash: crc mismatch")

	// ErrFrameTooLarge indicates a frame exceeded maxFrameSize.
	ErrFrameTooLarge = errors.New("ash: frame too large")

	// ErrConnectionLost indicates 5 consecutive retransmission failures.
	ErrConnectionLost = errors.New("ash: connection lost")
)

// frameType returns the type of a frame from its control byte.
func frameType(control byte) byte {
	if control&0x80 == 0 {
		return frameTypeDATA
	}
	switch {
	case control >= frameTypeRST:
		return control // RST, RSTACK, ERROR have fixed control bytes
	case control&0xE0 == frameTypeNAK:
		return frameTypeNAK
	default:
		return frameTypeACK
	}
}

// CRC-CCITT lookup table (polynomial 0x1021, init 0xFFFF).
var crcTable [256]uint16

func init() {
	for i := range 256 {
		crc := uint16(i) << 8
		for range 8 {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ 0x1021
			} else {
				crc <<= 1
			}
		}
		crcTable[i] = crc
	}
}

// crcCCITT computes the CRC-CCITT over data with init value 0xFFFF.
func crcCCITT(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc = (crc << 8) ^ crcTable[byte(crc>>8)^b]
	}
	return crc
}

// stuff applies byte stuffing to data, escaping reserved bytes.
// Reserved bytes are replaced with [0x7D, byte^0x20].
func stuff(data []byte) []byte {
	out := make([]byte, 0, len(data)+len(data)/8)
	for _, b := range data {
		if isReserved(b) {
			out = append(out, byteEscape, b^0x20)
		} else {
			out = append(out, b)
		}
	}
	return out
}

// unstuff removes byte stuffing from data in-place.
// Returns the unstuffed data (a slice of the input buffer).
func unstuff(data []byte) []byte {
	w := 0
	for r := 0; r < len(data); r++ {
		if data[r] == byteEscape && r+1 < len(data) {
			r++
			data[w] = data[r] ^ 0x20
		} else {
			data[w] = data[r]
		}
		w++
	}
	return data[:w]
}

// isReserved returns true if b must be escaped during byte stuffing.
func isReserved(b byte) bool {
	switch b {
	case byteFlag, byteEscape, byteXON, byteXOFF, byteCancel, byteSubstitute:
		return true
	}
	return false
}

// randomize XORs data with the LFSR pseudo-random sequence.
// The LFSR resets to seed (0x42) for each frame.
// Per UG101 §4.3, this is applied to the data field (between control byte and CRC).
func randomize(data []byte) {
	lfsr := byte(lfsrSeed)
	for i := range data {
		data[i] ^= lfsr
		lfsr = nextLFSR(lfsr)
	}
}

// nextLFSR advances the LFSR by one step.
// Algorithm: shift right, XOR with polynomial if LSB was 1.
func nextLFSR(state byte) byte {
	lsb := state & 0x01
	state >>= 1
	if lsb != 0 {
		state ^= lfsrPolynomial
	}
	return state
}

// encodeRST returns a byte-stuffed RST frame with flag byte.
func encodeRST() []byte {
	frame := []byte{frameTypeRST}
	crc := crcCCITT(frame)
	frame = append(frame, byte(crc>>8), byte(crc))
	frame = stuff(frame)
	frame = append(frame, byteFlag)
	return frame
}

// cancelBytes returns n CANCEL bytes (0x1A) used to flush NCP buffer before RST.
func cancelBytes(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byteSubstitute
	}
	return b
}

// decodeFrame parses a raw unstuffed frame.
// It verifies CRC and returns the control byte and data payload.
// For DATA frames, the data field arrives randomized on the wire and the CRC
// covers [control + randomized data]. After CRC verification, the data is
// de-randomized before being returned to the caller.
func decodeFrame(raw []byte) (control byte, data []byte, err error) {
	if len(raw) < 3 {
		return 0, nil, errors.New("ash: frame too short")
	}

	control = raw[0]

	// CRC covers everything except the CRC bytes themselves.
	// For DATA frames this includes the still-randomized data.
	payload := raw[:len(raw)-2]
	crcHi := raw[len(raw)-2]
	crcLo := raw[len(raw)-1]
	want := uint16(crcHi)<<8 | uint16(crcLo)

	got := crcCCITT(payload)
	if got != want {
		return 0, nil, ErrCRC
	}

	data = raw[1 : len(raw)-2]

	// De-randomize data field for DATA frames (after CRC verification).
	if frameType(control) == frameTypeDATA {
		randomize(data) // XOR is self-inverse
	}

	return control, data, nil
}

// encodeDataFrame encodes a DATA frame with the given control byte and payload.
// Per UG101 §4.3, the data field is randomized before CRC computation.
// The CRC covers [control + randomized data] and is not itself randomized.
func encodeDataFrame(control byte, payload []byte) []byte {
	// Randomize the data field before CRC computation.
	randData := make([]byte, len(payload))
	copy(randData, payload)
	randomize(randData)

	// Build frame: control + randomized data
	frame := make([]byte, 0, 1+len(payload)+2)
	frame = append(frame, control)
	frame = append(frame, randData...)

	// Compute CRC over [control + randomized data]
	crc := crcCCITT(frame)
	frame = append(frame, byte(crc>>8), byte(crc))

	// Byte-stuff and append flag
	frame = stuff(frame)
	frame = append(frame, byteFlag)
	return frame
}

// encodeACK returns a byte-stuffed ACK frame.
func encodeACK(ackNum byte) []byte {
	control := frameTypeACK | (ackNum & 0x07)
	frame := []byte{control}
	crc := crcCCITT(frame)
	frame = append(frame, byte(crc>>8), byte(crc))
	// ACK frames are not randomized (no data field)
	frame = stuff(frame)
	frame = append(frame, byteFlag)
	return frame
}

// dataControlByte builds a DATA frame control byte.
// Per UG101: bits 6-4 = frmNum, bit 3 = reTx, bits 2-0 = ackNum.
func dataControlByte(frmNum, ackNum byte, reTx bool) byte {
	b := (frmNum & 0x07) << 4
	b |= ackNum & 0x07
	if reTx {
		b |= 0x08
	}
	return b
}

// parseDataControl extracts fields from a DATA frame control byte.
// Per UG101: bits 6-4 = frmNum, bit 3 = reTx, bits 2-0 = ackNum.
func parseDataControl(control byte) (frmNum, ackNum byte, reTx bool) {
	frmNum = (control >> 4) & 0x07
	ackNum = control & 0x07
	reTx = control&0x08 != 0
	return
}

// parseACKControl extracts the ackNum from an ACK/NAK control byte.
func parseACKControl(control byte) (ackNum byte, nRdy bool) {
	ackNum = control & 0x07
	nRdy = control&0x08 != 0
	return
}
