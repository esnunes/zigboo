// Package host manages the EZSP session lifecycle for a Zigbee host application.
//
// The NCP (network co-processor) is the Zigbee coordinator; this package
// implements the host side. It owns a single goroutine that serializes all
// access to the ASH connection, dispatches asynchronous NCP callbacks to
// registered handlers, and routes command responses back to callers.
package host

import (
	"context"
	"encoding/binary"
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
	msgTag   byte
	mu       sync.Mutex // protects seq and msgTag

	callbacks   map[uint16]callbackHandler
	msgMu       sync.RWMutex
	msgHandlers map[messageHandlerKey]func(msg IncomingMessage)
	cmdCh       chan commandRequest

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New creates a new Host for the given ASH connection.
// Set extended to true for EZSP v9+ extended frame format.
func New(conn *ash.Conn, extended bool) *Host {
	return &Host{
		conn:        conn,
		extended:    extended,
		callbacks:   make(map[uint16]callbackHandler),
		msgHandlers: make(map[messageHandlerKey]func(msg IncomingMessage)),
		cmdCh:       make(chan commandRequest),
	}
}

// OnCallback registers a handler for the given EZSP callback frame ID.
// Must be called before Start.
func (h *Host) OnCallback(frameID uint16, fn callbackHandler) {
	h.callbacks[frameID] = fn
}

// OnMessage registers a handler for incoming APS messages matching the given
// profile and cluster ID. Safe to call before or after Start.
func (h *Host) OnMessage(profileID, clusterID uint16, fn func(msg IncomingMessage)) {
	h.msgMu.Lock()
	defer h.msgMu.Unlock()
	h.msgHandlers[messageHandlerKey{profileID, clusterID}] = fn
}

// Start begins the reader goroutine that serializes ASH access.
// It also registers internal callbacks for message routing.
func (h *Host) Start(ctx context.Context) {
	h.OnCallback(ezsp.FrameIDIncomingMessageHandler, h.handleIncomingMessage)
	h.OnCallback(ezsp.FrameIDMessageSentHandler, h.handleMessageSent)

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

// AddEndpoint registers a local endpoint on the NCP.
func (h *Host) AddEndpoint(ctx context.Context, endpoint uint8, profileID, deviceID uint16, inputClusters, outputClusters []uint16) error {
	// endpoint(1) + profileId(2) + deviceId(2) + appFlags(1) + inputCount(1) + outputCount(1) + clusters
	params := make([]byte, 8+len(inputClusters)*2+len(outputClusters)*2)
	params[0] = endpoint
	binary.LittleEndian.PutUint16(params[1:3], profileID)
	binary.LittleEndian.PutUint16(params[3:5], deviceID)
	params[5] = 0x00 // appFlags: deviceVersion=0
	params[6] = byte(len(inputClusters))
	params[7] = byte(len(outputClusters))
	off := 8
	for _, c := range inputClusters {
		binary.LittleEndian.PutUint16(params[off:off+2], c)
		off += 2
	}
	for _, c := range outputClusters {
		binary.LittleEndian.PutUint16(params[off:off+2], c)
		off += 2
	}

	resp, err := h.Command(ctx, ezsp.FrameIDAddEndpoint, params)
	if err != nil {
		return fmt.Errorf("host: addEndpoint: %w", err)
	}
	if len(resp) < 1 || resp[0] != 0x00 {
		return fmt.Errorf("host: addEndpoint: EZSP status 0x%02X", resp[0])
	}
	return nil
}

// SendUnicast sends an APS unicast message to a destination node.
func (h *Host) SendUnicast(ctx context.Context, destID uint16, apsFrame ezsp.EmberApsFrame, payload []byte) error {
	tag := h.nextMsgTag()

	apsBytes := ezsp.EncodeApsFrame(apsFrame)
	// type(1) + destination(2) + apsFrame(11) + tag(1) + msgLen(1) + payload
	params := make([]byte, 0, 16+len(payload))
	params = append(params, 0x00) // EMBER_OUTGOING_DIRECT
	params = append(params, byte(destID), byte(destID>>8))
	params = append(params, apsBytes...)
	params = append(params, tag)
	params = append(params, byte(len(payload)))
	params = append(params, payload...)

	resp, err := h.Command(ctx, ezsp.FrameIDSendUnicast, params)
	if err != nil {
		return fmt.Errorf("host: sendUnicast: %w", err)
	}
	if len(resp) < 1 || resp[0] != 0x00 {
		return fmt.Errorf("host: sendUnicast: ember status 0x%02X", resp[0])
	}
	return nil
}

// handleIncomingMessage decodes and routes an incomingMessageHandler callback.
func (h *Host) handleIncomingMessage(params []byte) {
	msg, err := decodeIncomingMessage(params)
	if err != nil {
		slog.Debug("host: decode incoming message", "err", err)
		return
	}

	key := messageHandlerKey{msg.ApsFrame.ProfileID, msg.ApsFrame.ClusterID}
	h.msgMu.RLock()
	fn, ok := h.msgHandlers[key]
	h.msgMu.RUnlock()
	if ok {
		fn(msg)
	} else {
		slog.Debug("host: unhandled incoming message",
			"profileID", fmt.Sprintf("0x%04X", msg.ApsFrame.ProfileID),
			"clusterID", fmt.Sprintf("0x%04X", msg.ApsFrame.ClusterID))
	}
}

// handleMessageSent logs delivery status from messageSentHandler callbacks.
func (h *Host) handleMessageSent(params []byte) {
	// type(1) + indexOrDest(2) + apsFrame(11) + tag(1) + status(1) + msgLen(1) = 17
	if len(params) < 17 {
		slog.Debug("host: messageSent too short", "len", len(params))
		return
	}
	tag := params[14]
	status := params[15]
	if status != 0x00 {
		slog.Debug("host: message delivery failed",
			"tag", tag,
			"status", fmt.Sprintf("0x%02X", status))
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

// nextMsgTag returns the next APS message tag (uint8, monotonic, wrapping).
func (h *Host) nextMsgTag() byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	tag := h.msgTag
	h.msgTag++
	return tag
}
