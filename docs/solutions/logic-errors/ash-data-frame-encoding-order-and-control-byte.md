---
title: "Fix DATA frame encoding: correct randomization/CRC order and frmNum/ackNum bit positions"
date: 2026-03-02
category: logic-errors
tags: [ash, ezsp, zigbee, serial-protocol, crc, randomization, frame-encoding, zbdongle-e]
component: ash/frame.go
severity: high
symptoms:
  - NAK received on every DATA frame during phase 1 EZSP version negotiation
  - Retransmission failures leading to context deadline exceeded
  - NAK received on phase 2 DATA frame (frmNum=1) after phase 1 succeeded
root_causes:
  - CRC was computed over plain data then data+CRC randomized together; correct order is randomize data first then compute CRC over control byte plus randomized data with CRC appended unrandomized
  - ackNum and frmNum bit positions in DATA control byte were swapped relative to UG101 spec (ackNum should be bits 2-0 and frmNum bits 6-4, not the reverse)
related_specs:
  - "UG101: UART-UART Gateway Protocol Reference (Silicon Labs)"
---

# ASH DATA Frame Encoding: CRC/Randomization Order and Control Byte Bit Layout

## Symptoms

Running `zigboo -v --port /dev/cu.usbserial-1420 version` against a ZBDongle-E produced:

- **Phase 1:** NAK on every outgoing DATA frame. Retransmissions exhausted, context deadline exceeded.
- **Phase 2 (after fixing bug 1):** Phase 1 succeeded, but NAK on the second DATA frame (frmNum=1). Same retransmission failure.

## Investigation Steps

1. **Protocol trace mismatch against bellows.** Outgoing DATA frames were NAKed by the NCP. Frame encoding output was compared byte-by-byte against the bellows Python library (zigpy's ASH implementation). Two discrepancies were identified.

2. **CRC/randomization ordering.** Reading the bellows encoder revealed it randomizes the data field *first*, then computes CRC over `[control + randomized data]`. The CRC bytes are never randomized. The old zigboo implementation did the opposite: computed CRC over plain data, then randomized data+CRC together.

3. **Control byte bit positions.** UG101 Section 3.1 specifies: bits 6-4 = frmNum, bit 3 = reTx, bits 2-0 = ackNum. The old implementation had frmNum and ackNum swapped. This was invisible when both were 0 (phase 1), but caused sequence number mismatches in phase 2.

## Root Cause Analysis

### Bug 1: Wrong randomization/CRC order

UG101 Section 4.3 specifies that data randomization is applied *before* CRC computation. The CRC covers `[control + randomized data]` and the CRC bytes are **not** randomized.

The old code:
- **Encode:** CRC over plaintext, then randomize data+CRC together
- **Decode:** De-randomize entire frame in reader goroutine, then check CRC

This produced CRCs that didn't match what the NCP expected.

### Bug 2: Swapped frmNum/ackNum bits

UG101 Table 3 defines the DATA control byte:

| Bits 6-4 | Bit 3 | Bits 2-0 |
|----------|-------|----------|
| frmNum   | reTx  | ackNum   |

The old implementation placed ackNum in bits 6-4 and frmNum in bits 2-0. When parsing NCP response `0x01` (frmNum=0, ackNum=1), the swapped parser read frmNum=1, causing phase 2 to encode `0x21` instead of `0x11`.

## Solution

### Bug 1: `encodeDataFrame` and `decodeFrame` (`ash/frame.go`)

**Encode:** Randomize data first, then CRC over `[control + randomized data]`:

```go
func encodeDataFrame(control byte, payload []byte) []byte {
    randData := make([]byte, len(payload))
    copy(randData, payload)
    randomize(randData)

    frame := make([]byte, 0, 1+len(payload)+2)
    frame = append(frame, control)
    frame = append(frame, randData...)

    crc := crcCCITT(frame)
    frame = append(frame, byte(crc>>8), byte(crc))

    frame = stuff(frame)
    frame = append(frame, byteFlag)
    return frame
}
```

**Decode:** CRC check on still-randomized data, then de-randomize DATA frames only:

```go
func decodeFrame(raw []byte) (control byte, data []byte, err error) {
    // ... length check ...
    control = raw[0]
    payload := raw[:len(raw)-2]
    // CRC verification on raw (randomized) frame
    got := crcCCITT(payload)
    if got != want { return 0, nil, ErrCRC }
    data = raw[1 : len(raw)-2]
    // De-randomize only DATA frames, after CRC passes
    if frameType(control) == frameTypeDATA {
        randomize(data) // XOR is self-inverse
    }
    return control, data, nil
}
```

**Reader goroutine (`ash/ash.go`):** Removed de-randomization; raw unstuffed frames go directly to `decodeFrame`.

### Bug 2: `dataControlByte` and `parseDataControl` (`ash/frame.go`)

Swapped bit positions to match UG101:

```go
func dataControlByte(frmNum, ackNum byte, reTx bool) byte {
    b := (frmNum & 0x07) << 4  // was: (ackNum & 0x07) << 4
    b |= ackNum & 0x07         // was: frmNum & 0x07
    if reTx { b |= 0x08 }
    return b
}

func parseDataControl(control byte) (frmNum, ackNum byte, reTx bool) {
    frmNum = (control >> 4) & 0x07  // was: ackNum extraction
    ackNum = control & 0x07         // was: frmNum extraction
    reTx = control&0x08 != 0
    return
}
```

## Prevention Strategies

### Reference implementation cross-checking

The primary prevention is `TestEncodeDataFrameAgainstBellowsReference` in `ash/frame_test.go`. This test uses a completely independent inline implementation of CRC, LFSR, and byte stuffing (no shared code with production) to verify that `encodeDataFrame` produces identical wire bytes to what bellows would produce. Test vectors use asymmetric, non-zero field values to detect transpositions.

### Why the original tests missed both bugs

- **Bug 1:** Unit tests verified CRC and LFSR independently (both correct), but never checked pipeline ordering against an external reference.
- **Bug 2:** Test cases used frmNum=0, ackNum=0. Swapping two zeros is a no-op, so the test passed with discriminating power of zero.

### Best practices for protocol implementations

1. **Test against an external reference.** Your own reading of the spec is a single point of failure.
2. **Use asymmetric, non-zero test inputs.** Every field should have a distinct value so swaps are detectable.
3. **Test the assembled pipeline, not just individual stages.** Integration tests catch ordering bugs that unit tests miss.
4. **Capture real wire traffic as golden vectors** when hardware is available.

## Key Takeaways

- Both bugs were spec-misreading bugs, not logic bugs. The code did exactly what the author intended; the author's understanding was wrong.
- Tests derived from the same misunderstanding cannot catch these bugs. Only external references provide triangulation.
- Degenerate test cases (all zeros, identity values) provide false confidence. Always test with inputs that make incorrect implementations produce visibly different outputs.

## Related References

- [UG101: UART-EZSP Gateway Protocol Reference](https://www.silabs.com/documents/public/user-guides/ug101-uart-gateway-protocol-reference.pdf) - ASH spec
- [bellows (zigpy)](https://github.com/zigpy/bellows) - Python ASH/EZSP reference implementation
- [Brainstorm: Zigbee Serial Communication](../brainstorms/2026-03-01-zigbee-serial-communication-brainstorm.md)
- [Plan: ASH/EZSP Serial Communication](../plans/2026-03-01-feat-ash-ezsp-serial-communication-plan.md)

## Files Changed

- `ash/frame.go` - Fixed `encodeDataFrame`, `decodeFrame`, `dataControlByte`, `parseDataControl`
- `ash/ash.go` - Removed de-randomization from reader goroutine
- `ash/frame_test.go` - Added `TestEncodeDataFrameAgainstBellowsReference`, updated test vectors
