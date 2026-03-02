package ash

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/esnunes/zigboo/serial"
)

const (
	// readerBufSize is the size of the buffer used for serial port reads.
	readerBufSize = 256

	// frameChanCap is the capacity of the received frames channel.
	frameChanCap = 8

	// cancelCount is the number of CANCEL bytes sent before RST.
	cancelCount = 32

	// resetTimeout is the maximum time to wait for RSTACK after sending RST.
	resetTimeout = 5 * time.Second

	// maxResetRetries is the number of RST retries before giving up.
	maxResetRetries = 3

	// retransmitTimeout is the fixed retransmission timeout for DATA frames.
	retransmitTimeout = 1600 * time.Millisecond

	// maxRetransmits is the maximum consecutive failures before connection lost.
	maxRetransmits = 5
)

// Conn manages an ASH connection over a serial port.
type Conn struct {
	port   serial.Port
	frames chan []byte // received frames from reader goroutine
	wg     sync.WaitGroup
	cancel context.CancelFunc

	// Sequence tracking (only accessed from Send, which is not concurrent).
	frmNum byte // next outgoing frame number (0-7)
	ackNum byte // next expected incoming frame number (0-7)
}

// New creates a new ASH connection over the given serial port.
// The reader goroutine is started immediately. Call Close to shut down.
func New(port serial.Port) *Conn {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Conn{
		port:   port,
		frames: make(chan []byte, frameChanCap),
		cancel: cancel,
	}

	c.wg.Add(1)
	go c.reader(ctx)

	return c
}

// Close shuts down the connection and waits for the reader goroutine to exit.
func (c *Conn) Close() error {
	c.cancel()
	c.wg.Wait()
	return nil
}

// Reset performs the ASH RST/RSTACK handshake.
//
// It flushes the serial buffer, sends CANCEL bytes followed by an RST frame,
// and waits for an RSTACK response. On success, it resets sequence numbers.
func (c *Conn) Reset(ctx context.Context) (version, resetCode byte, err error) {
	// Flush any stale data in the serial buffer.
	if err := c.port.Flush(); err != nil {
		slog.Warn("ash: flush before reset failed", "err", err)
	}

	// Drain any frames already in the channel.
	for {
		select {
		case <-c.frames:
		default:
			goto drained
		}
	}
drained:

	for attempt := range maxResetRetries {
		slog.Debug("ash: sending RST", "attempt", attempt+1)

		// Send CANCEL bytes to force NCP to discard partial frames.
		if _, err := c.port.Write(cancelBytes(cancelCount)); err != nil {
			return 0, 0, fmt.Errorf("ash: reset: write cancel: %w", err)
		}

		// Send RST frame.
		if _, err := c.port.Write(encodeRST()); err != nil {
			return 0, 0, fmt.Errorf("ash: reset: write rst: %w", err)
		}

		// Wait for RSTACK.
		timer := time.NewTimer(resetTimeout)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0, 0, ctx.Err()
		case raw, ok := <-c.frames:
			timer.Stop()
			if !ok {
				return 0, 0, fmt.Errorf("ash: reset: reader closed")
			}

			control, data, err := decodeFrame(raw)
			if err != nil {
				slog.Debug("ash: reset: decode error, retrying", "err", err)
				continue
			}

			switch frameType(control) {
			case frameTypeRSTACK:
				if len(data) < 2 {
					slog.Debug("ash: reset: RSTACK too short", "len", len(data))
					continue
				}
				version = data[0]
				resetCode = data[1]
				c.frmNum = 0
				c.ackNum = 0
				slog.Debug("ash: RSTACK received", "version", version, "resetCode", resetCode)
				return version, resetCode, nil

			case frameTypeERROR:
				if len(data) >= 3 {
					return 0, 0, fmt.Errorf("ash: reset: NCP error (version=%d, resetCode=%d, errorCode=%d)",
						data[0], data[1], data[2])
				}
				return 0, 0, fmt.Errorf("ash: reset: NCP error frame received")

			default:
				slog.Debug("ash: reset: unexpected frame type", "control", fmt.Sprintf("0x%02X", control))
			}

		case <-timer.C:
			slog.Debug("ash: reset: timeout, retrying", "attempt", attempt+1)
		}
	}

	return 0, 0, fmt.Errorf("ash: reset: no response after %d attempts — check baud rate (expected 115200) and device path", maxResetRetries)
}

// Send transmits an EZSP payload over ASH and returns the response payload.
//
// It sends a DATA frame, waits for an ACK and the corresponding response DATA
// frame from the NCP. Retransmission is handled automatically up to
// maxRetransmits consecutive failures.
func (c *Conn) Send(ctx context.Context, data []byte) ([]byte, error) {
	control := dataControlByte(c.frmNum, c.ackNum, false)
	frame := encodeDataFrame(control, data)

	for failures := 0; failures < maxRetransmits; failures++ {
		if failures > 0 {
			// Retransmission: set reTx bit.
			control = dataControlByte(c.frmNum, c.ackNum, true)
			frame = encodeDataFrame(control, data)
			slog.Debug("ash: retransmitting DATA", "frmNum", c.frmNum, "attempt", failures+1)
		}

		if _, err := c.port.Write(frame); err != nil {
			return nil, fmt.Errorf("ash: send: write: %w", err)
		}

		response, err := c.waitForResponse(ctx)
		if err == ErrTimeout {
			continue
		}
		if err != nil {
			return nil, err
		}
		return response, nil
	}

	return nil, ErrConnectionLost
}

// waitForResponse waits for an ACK and response DATA frame from the NCP.
func (c *Conn) waitForResponse(ctx context.Context) ([]byte, error) {
	acked := false
	timer := time.NewTimer(retransmitTimeout)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()

		case raw, ok := <-c.frames:
			if !ok {
				return nil, fmt.Errorf("ash: send: reader closed")
			}

			control, payload, err := decodeFrame(raw)
			if err != nil {
				slog.Debug("ash: send: decode error, ignoring", "err", err)
				continue
			}

			switch frameType(control) {
			case frameTypeACK:
				ackNum, _ := parseACKControl(control)
				// ACK acknowledges our frame if ackNum == frmNum+1 (mod 8).
				expectedAck := (c.frmNum + 1) & 0x07
				if ackNum == expectedAck {
					acked = true
					slog.Debug("ash: ACK received", "ackNum", ackNum)
				}

			case frameTypeNAK:
				slog.Debug("ash: NAK received, will retransmit")
				return nil, ErrTimeout // triggers retransmission

			case frameTypeDATA:
				frmNum, _, _ := parseDataControl(control)

				// De-randomize the payload (everything after control byte was randomized).
				// Note: payload is already the de-randomized data from decodeDataFrame.
				// Actually, we need to de-randomize at the frame level.
				// The raw frame from the reader has already been unstuffed but NOT de-randomized.
				// De-randomize now.

				// Send ACK for this frame.
				nextAck := (frmNum + 1) & 0x07
				c.ackNum = nextAck
				ackFrame := encodeACK(nextAck)
				if _, err := c.port.Write(ackFrame); err != nil {
					slog.Warn("ash: send ACK failed", "err", err)
				}

				// Advance our frame number.
				c.frmNum = (c.frmNum + 1) & 0x07

				return payload, nil

			case frameTypeRSTACK:
				return nil, ErrConnectionReset

			default:
				slog.Debug("ash: send: unexpected frame", "control", fmt.Sprintf("0x%02X", control))
			}

		case <-timer.C:
			if acked {
				// ACKed but no response DATA yet — keep waiting with a fresh timer.
				timer.Reset(retransmitTimeout)
				continue
			}
			return nil, ErrTimeout
		}
	}
}

// reader reads bytes from the serial port and assembles complete frames.
// Frames are delimited by 0x7E flag bytes. Complete frames are sent on the
// frames channel after unstuffing and de-randomization.
func (c *Conn) reader(ctx context.Context) {
	defer c.wg.Done()
	defer close(c.frames)

	var buf [readerBufSize]byte
	var frameBuf [maxFrameSize]byte
	frameLen := 0

	for {
		// Check for cancellation between reads.
		select {
		case <-ctx.Done():
			return
		default:
		}

		n, err := c.port.Read(buf[:])
		if err != nil {
			// Check if we're shutting down.
			select {
			case <-ctx.Done():
				return
			default:
			}
			slog.Debug("ash: reader: read error", "err", err)
			return
		}

		for i := range n {
			b := buf[i]

			switch b {
			case byteFlag:
				if frameLen > 0 {
					// Complete frame received.
					raw := make([]byte, frameLen)
					copy(raw, frameBuf[:frameLen])

					// Unstuff the frame.
					raw = unstuff(raw)

					if len(raw) >= 3 {
						// De-randomize: for DATA frames, de-randomize everything
						// after the control byte.
						control := raw[0]
						if frameType(control) == frameTypeDATA && len(raw) > 1 {
							randomize(raw[1:]) // XOR is self-inverse
						}

						// Send to consumer, respecting cancellation.
						select {
						case c.frames <- raw:
						case <-ctx.Done():
							return
						}
					}

					frameLen = 0
				}

			case byteSubstitute:
				// Cancel byte — discard current frame buffer.
				frameLen = 0

			case byteCancel:
				// Cancel byte — discard current frame buffer.
				frameLen = 0

			default:
				if frameLen < maxFrameSize {
					frameBuf[frameLen] = b
					frameLen++
				} else {
					// Frame too large — discard and resynchronize.
					slog.Debug("ash: reader: frame exceeded max size, discarding")
					frameLen = 0
				}
			}
		}
	}
}
