// Package host manages the EZSP session lifecycle for a Zigbee host application.
//
// The NCP (network co-processor) is the Zigbee coordinator; this package
// implements the host side. It owns a single goroutine that serializes all
// access to the ASH connection, dispatches asynchronous NCP callbacks to
// registered handlers, and routes command responses back to callers.
package host

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/esnunes/zigboo/ash"
	"github.com/esnunes/zigboo/ezsp"
)

const (
	// commandTimeout is the maximum time to wait for an EZSP command response.
	commandTimeout = 5 * time.Second

	// pollTimeout is the idle callback polling interval.
	pollTimeout = 50 * time.Millisecond
)

// callbackHandler is a function that handles an EZSP callback.
type callbackHandler func(params []byte)

// commandRequest is sent to the run goroutine to execute an EZSP command.
type commandRequest struct {
	ctx      context.Context
	frameID  uint16
	params   []byte
	resultCh chan commandResult
}

// commandResult holds the response or error from an EZSP command.
type commandResult struct {
	params []byte
	err    error
}

// Host manages the EZSP session and callback dispatch.
type Host struct {
	conn     *ash.Conn
	extended bool
	seq      byte
	mu       sync.Mutex // protects seq

	callbacks map[uint16]callbackHandler
	cmdCh     chan commandRequest

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New creates a new Host for the given ASH connection.
// Set extended to true for EZSP v9+ extended frame format.
func New(conn *ash.Conn, extended bool) *Host {
	return &Host{
		conn:      conn,
		extended:  extended,
		callbacks: make(map[uint16]callbackHandler),
		cmdCh:     make(chan commandRequest),
	}
}

// OnCallback registers a handler for the given EZSP callback frame ID.
// Must be called before Start.
func (h *Host) OnCallback(frameID uint16, fn callbackHandler) {
	h.callbacks[frameID] = fn
}

// Start begins the reader goroutine that serializes ASH access.
func (h *Host) Start(ctx context.Context) {
	ctx, h.cancel = context.WithCancel(ctx)
	h.wg.Add(1)
	go h.run(ctx)
}

// Close stops the reader goroutine and waits for it to exit.
func (h *Host) Close() {
	if h.cancel != nil {
		h.cancel()
	}
	h.wg.Wait()
}

// Command sends an EZSP command and returns the response parameters.
// It blocks until the response is received or the context is cancelled.
func (h *Host) Command(ctx context.Context, frameID uint16, params []byte) ([]byte, error) {
	req := commandRequest{
		ctx:      ctx,
		frameID:  frameID,
		params:   params,
		resultCh: make(chan commandResult, 1),
	}

	select {
	case h.cmdCh <- req:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	select {
	case result := <-req.resultCh:
		return result.params, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// run is the main goroutine that serializes all ASH access.
// It alternates between processing commands and polling for callbacks.
func (h *Host) run(ctx context.Context) {
	defer h.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return

		case req := <-h.cmdCh:
			h.executeCommand(ctx, req)

		default:
			// Poll for callbacks between commands.
			h.pollCallback(ctx)
		}
	}
}

// executeCommand sends an EZSP command via ASH and returns the response.
// Callbacks received before the matching response are dispatched.
func (h *Host) executeCommand(ctx context.Context, req commandRequest) {
	frame := ezsp.EncodeFrame(h.extended, h.nextSeq(), req.frameID, req.params)

	// Use the request's context for the timeout, but also respect our own context.
	cmdCtx, cancel := context.WithTimeout(req.ctx, commandTimeout)
	defer cancel()

	// Send the frame (this sends DATA, waits for ACK, and returns the first
	// DATA response — which may be a callback or the command response).
	resp, err := h.conn.Send(cmdCtx, frame)
	if err != nil {
		req.resultCh <- commandResult{nil, err}
		return
	}

	// Decode and dispatch: skip callbacks until we find the matching response.
	for {
		_, respFrameID, respParams, decErr := ezsp.DecodeFrame(resp)
		if decErr != nil {
			req.resultCh <- commandResult{nil, fmt.Errorf("host: decode response: %w", decErr)}
			return
		}

		if respFrameID == req.frameID {
			req.resultCh <- commandResult{respParams, nil}
			return
		}

		// This is a callback — dispatch it.
		h.dispatchCallback(respFrameID, respParams)

		// Read the next frame (the actual command response).
		resp, err = h.conn.Recv(cmdCtx)
		if err != nil {
			req.resultCh <- commandResult{nil, fmt.Errorf("host: recv after callback: %w", err)}
			return
		}
	}
}

// pollCallback tries to receive a callback from the NCP with a short timeout.
func (h *Host) pollCallback(ctx context.Context) {
	pollCtx, cancel := context.WithTimeout(ctx, pollTimeout)
	defer cancel()

	raw, err := h.conn.Recv(pollCtx)
	if err != nil {
		// Timeout is expected — no callback available.
		return
	}

	_, frameID, params, decErr := ezsp.DecodeFrame(raw)
	if decErr != nil {
		slog.Debug("host: decode callback error", "err", decErr)
		return
	}

	h.dispatchCallback(frameID, params)
}

// dispatchCallback routes a callback to its registered handler.
func (h *Host) dispatchCallback(frameID uint16, params []byte) {
	if fn, ok := h.callbacks[frameID]; ok {
		fn(params)
	} else {
		slog.Debug("host: unhandled callback",
			"frameID", fmt.Sprintf("0x%04X", frameID))
	}
}

// nextSeq returns the next EZSP sequence number (uint8, monotonic, wrapping).
func (h *Host) nextSeq() byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	seq := h.seq
	h.seq++
	return seq
}
