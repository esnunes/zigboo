package ezsp

import "encoding/binary"

// EZSP frame format constants.
const (
	// ExtendedVersionThreshold is the EZSP version at which the extended
	// frame format is required. Versions below this use legacy format.
	ExtendedVersionThreshold = 9

	// extendedFormatMarker is the value of fc_hi in extended format frames,
	// identifying them as extended format.
	extendedFormatMarker = 0x01
)

// EncodeLegacy encodes an EZSP frame in legacy format (v4-v8).
// Legacy format: [seq(1)] [fc(1)] [frameID(1)] [params...]
func EncodeLegacy(seq byte, frameID uint16, params []byte) []byte {
	frame := make([]byte, 0, 3+len(params))
	frame = append(frame, seq, 0x00, byte(frameID))
	frame = append(frame, params...)
	return frame
}

// EncodeExtended encodes an EZSP frame in extended format (v9+).
// Extended format: [seq(1)] [fc_lo(1)] [fc_hi(1)] [frameID_lo(1)] [frameID_hi(1)] [params...]
func EncodeExtended(seq byte, frameID uint16, params []byte) []byte {
	frame := make([]byte, 0, 5+len(params))
	frame = append(frame, seq, 0x00, extendedFormatMarker)
	frame = append(frame, byte(frameID), byte(frameID>>8))
	frame = append(frame, params...)
	return frame
}

// DecodeLegacy decodes an EZSP frame in legacy format.
// Returns the sequence number, frame ID, and parameters.
func DecodeLegacy(data []byte) (seq byte, frameID uint16, params []byte, err error) {
	if len(data) < 3 {
		return 0, 0, nil, ErrFrameTooShort
	}
	seq = data[0]
	// data[1] is frame control (ignored for now)
	frameID = uint16(data[2])
	if len(data) > 3 {
		params = data[3:]
	}
	return seq, frameID, params, nil
}

// DecodeExtended decodes an EZSP frame in extended format.
// Returns the sequence number, frame ID, and parameters.
func DecodeExtended(data []byte) (seq byte, frameID uint16, params []byte, err error) {
	if len(data) < 5 {
		return 0, 0, nil, ErrFrameTooShort
	}
	seq = data[0]
	// data[1:3] is frame control (fc_lo, fc_hi=0x01)
	frameID = binary.LittleEndian.Uint16(data[3:5])
	if len(data) > 5 {
		params = data[5:]
	}
	return seq, frameID, params, nil
}

// IsExtendedFormat returns true if the frame uses the extended format.
// The fc_hi byte (data[2]) equals extendedFormatMarker (0x01) in extended format.
func IsExtendedFormat(data []byte) bool {
	return len(data) >= 3 && data[2] == extendedFormatMarker
}

// EncodeFrame encodes an EZSP frame in the appropriate format.
func EncodeFrame(extended bool, seq byte, frameID uint16, params []byte) []byte {
	if extended {
		return EncodeExtended(seq, frameID, params)
	}
	return EncodeLegacy(seq, frameID, params)
}

// DecodeFrame decodes an EZSP frame, automatically detecting the format.
// Returns the sequence number, frame ID, and parameters.
func DecodeFrame(data []byte) (seq byte, frameID uint16, params []byte, err error) {
	if IsExtendedFormat(data) {
		return DecodeExtended(data)
	}
	return DecodeLegacy(data)
}
