package polymarketevents

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

type PolymarketEventsClient struct {
	logger *zap.Logger

	marketWSURL          string
	dialer               *websocket.Dialer
	pingInterval         time.Duration
	customFeatureEnabled bool

	connMu  sync.Mutex
	writeMu sync.Mutex
	conn    *websocket.Conn

	msgCh   chan json.RawMessage
	errCh   chan error
	closeCh chan struct{}

	msgCount        uint64
	lastMsgUnixNano int64
}

func NewPolymarketEventsClient(logger *zap.Logger) *PolymarketEventsClient {
	if logger == nil {
		logger = zap.NewNop()
	}

	return &PolymarketEventsClient{
		logger:               logger,
		marketWSURL:          "wss://ws-subscriptions-clob.polymarket.com/ws/market",
		dialer:               websocket.DefaultDialer,
		pingInterval:         10 * time.Second,
		customFeatureEnabled: true,

		msgCh:   make(chan json.RawMessage, 1024),
		errCh:   make(chan error, 64),
		closeCh: make(chan struct{}),
	}
}

// ConnectMarket dials the public market channel and subscribes to the provided
// asset IDs (token IDs).
//
// Note: market channel is public; no API key required.
func (c *PolymarketEventsClient) ConnectMarket(
	ctx context.Context,
	assetIDs []string,
) error {
	c.connMu.Lock()
	alreadyConnected := c.conn != nil
	c.connMu.Unlock()
	if alreadyConnected {
		return fmt.Errorf("already connected")
	}

	conn, _, err := c.dialer.DialContext(ctx, c.marketWSURL, nil)
	if err != nil {
		return fmt.Errorf("dial market ws: %w", err)
	}

	c.logger.Info(
		"polymarket ws dialed",
		zap.String("url", c.marketWSURL),
		zap.Int("assets", len(assetIDs)),
	)

	conn.SetCloseHandler(func(code int, text string) error {
		c.logger.Warn(
			"polymarket ws close frame received",
			zap.Int("code", code),
			zap.String("reason", text),
		)
		return nil
	})

	c.connMu.Lock()
	c.conn = conn
	c.connMu.Unlock()

	// Per docs:
	// { "assets_ids": [...], "type": "market" }
	// custom_feature_enabled is optional; you can remove it if desired.
	sub := map[string]any{
		"type":       "market",
		"assets_ids": assetIDs,
	}
	if c.customFeatureEnabled {
		sub["custom_feature_enabled"] = true
	}

	c.logger.Info("polymarket ws subscribing", zap.Any("payload", sub))

	if err := c.writeJSON(sub); err != nil {
		_ = conn.Close()
		c.connMu.Lock()
		c.conn = nil
		c.connMu.Unlock()
		return fmt.Errorf("send initial subscription: %w", err)
	}

	c.logger.Info("polymarket ws subscription sent")

	go c.readLoop()
	go c.pingLoop()

	go func() {
		select {
		case <-ctx.Done():
			_ = c.Close()
		case <-c.closeCh:
		}
	}()

	return nil
}

func (c *PolymarketEventsClient) SubscribeAssets(assetIDs []string) error {
	return c.sendOp("subscribe", assetIDs)
}

func (c *PolymarketEventsClient) UnsubscribeAssets(assetIDs []string) error {
	return c.sendOp("unsubscribe", assetIDs)
}

func (c *PolymarketEventsClient) Messages() <-chan json.RawMessage {
	return c.msgCh
}

func (c *PolymarketEventsClient) Errors() <-chan error {
	return c.errCh
}

type WSStats struct {
	MessageCount  uint64
	LastMessageAt time.Time
}

// TradeEvent represents a trade event from the WebSocket.
type TradeEvent struct {
	EventType       string  `json:"event_type"`
	AssetID         string  `json:"asset_id"`
	Price           string  `json:"price"`
	Size            string  `json:"size"`
	Side            string  `json:"side"`
	MakerAddress    string  `json:"maker_address"`
	TakerAddress    string  `json:"taker_address"`
	Timestamp       string  `json:"timestamp"`
	TransactionHash string  `json:"transaction_hash"`
	FeeRateBps      string  `json:"fee_rate_bps"`
	TradeID         string  `json:"id"`
}

// ParseTradeEvent attempts to parse a JSON message as a TradeEvent.
// Returns nil if the message is not a trade event.
func ParseTradeEvent(data json.RawMessage) *TradeEvent {
	var event TradeEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return nil
	}
	// Check if it's actually a trade event
	if event.EventType != "trade" && event.EventType != "last_trade_price" {
		return nil
	}
	return &event
}

// ParseEventType extracts just the event_type from a message for debugging.
func ParseEventType(data json.RawMessage) string {
	var m struct {
		EventType string `json:"event_type"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return "unknown"
	}
	if m.EventType == "" {
		return "empty"
	}
	return m.EventType
}

// GetPriceFloat returns the price as a float64.
func (e *TradeEvent) GetPriceFloat() float64 {
	var price float64
	fmt.Sscanf(e.Price, "%f", &price)
	return price
}

// GetSizeFloat returns the size as a float64.
func (e *TradeEvent) GetSizeFloat() float64 {
	var size float64
	fmt.Sscanf(e.Size, "%f", &size)
	return size
}

// GetTimestampUnix returns the timestamp as Unix seconds.
func (e *TradeEvent) GetTimestampUnix() int64 {
	var ts int64
	fmt.Sscanf(e.Timestamp, "%d", &ts)
	return ts
}

func (c *PolymarketEventsClient) Stats() WSStats {
	n := atomic.LoadUint64(&c.msgCount)
	ns := atomic.LoadInt64(&c.lastMsgUnixNano)

	var t time.Time
	if ns > 0 {
		t = time.Unix(0, ns)
	}

	return WSStats{
		MessageCount:  n,
		LastMessageAt: t,
	}
}

func (c *PolymarketEventsClient) Close() error {
	c.connMu.Lock()
	defer c.connMu.Unlock()

	// Signal goroutines to stop by closing closeCh
	select {
	case <-c.closeCh:
		// Channel was already closed
	default:
		close(c.closeCh)
	}

	// Create fresh channel for potential reconnection
	c.closeCh = make(chan struct{})

	var err error
	if c.conn != nil {
		err = c.conn.Close()
		c.conn = nil
	}

	return err
}

func (c *PolymarketEventsClient) sendOp(operation string, assetIDs []string) error {
	msg := map[string]any{
		"operation":  operation,
		"assets_ids": assetIDs,
	}
	if c.customFeatureEnabled {
		msg["custom_feature_enabled"] = true
	}

	c.logger.Info("polymarket ws op", zap.Any("payload", msg))
	return c.writeJSON(msg)
}

func (c *PolymarketEventsClient) writeJSON(v any) error {
	c.connMu.Lock()
	conn := c.conn
	c.connMu.Unlock()

	if conn == nil {
		return fmt.Errorf("not connected")
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	return conn.WriteJSON(v)
}

func (c *PolymarketEventsClient) pingLoop() {
	c.logger.Info(
		"polymarket ws ping loop started",
		zap.Duration("interval", c.pingInterval),
	)

	t := time.NewTicker(c.pingInterval)
	defer t.Stop()

	for {
		select {
		case <-t.C:
			c.connMu.Lock()
			conn := c.conn
			c.connMu.Unlock()

			if conn != nil {
				c.writeMu.Lock()
				_ = conn.WriteMessage(websocket.TextMessage, []byte("PING"))
				c.writeMu.Unlock()
			}

		case <-c.closeCh:
			return
		}
	}
}

func (c *PolymarketEventsClient) readLoop() {
	c.logger.Info("polymarket ws read loop started")

	first := true

	for {
		select {
		case <-c.closeCh:
			c.logger.Info("polymarket ws read loop exiting: closeCh signaled")
			return
		default:
		}

		c.connMu.Lock()
		conn := c.conn
		c.connMu.Unlock()

		if conn == nil {
			c.logger.Info("polymarket ws read loop exiting: conn is nil")
			return
		}

		_, b, err := conn.ReadMessage()
		if err != nil {
			c.logger.Warn("polymarket ws read loop exiting: read error", zap.Error(err))
			select {
			case c.errCh <- err:
			default:
			}
			_ = c.Close()
			return
		}

		// Server may reply with plain "PONG".
		if string(b) == "PONG" || string(b) == "PING" {
			continue
		}

		atomic.AddUint64(&c.msgCount, 1)
		atomic.StoreInt64(&c.lastMsgUnixNano, time.Now().UnixNano())

		if first {
			first = false
			c.logger.Info(
				"polymarket ws received first frame",
				zap.Int("bytes", len(b)),
				zap.ByteString("frame", b),
			)
		}

		// The server may send either:
		// - a single JSON object event
		// - a JSON array of events (batch)
		c.emitFrame(b)
	}
}

func (c *PolymarketEventsClient) emitFrame(b []byte) {
	trimmed := b
	for len(trimmed) > 0 && (trimmed[0] == ' ' || trimmed[0] == '\n' || trimmed[0] == '\t' || trimmed[0] == '\r') {
		trimmed = trimmed[1:]
	}

	if len(trimmed) == 0 {
		return
	}

	// Batch case: JSON array
	if trimmed[0] == '[' {
		var arr []json.RawMessage
		if err := json.Unmarshal(trimmed, &arr); err != nil {
			c.logger.Warn(
				"polymarket ws bad json array frame",
				zap.Error(err),
				zap.ByteString("frame", b),
			)
			return
		}

		if len(arr) == 0 {
			c.logger.Info("polymarket ws empty batch frame received")
			return
		}

		for _, one := range arr {
			c.forward(one)
		}
		return
	}

	// Single event case: JSON object (or something else)
	c.forward(json.RawMessage(append([]byte(nil), trimmed...)))
}

func (c *PolymarketEventsClient) forward(msg json.RawMessage) {
	select {
	case c.msgCh <- msg:
	default:
		c.logger.Warn("dropping ws message: msgCh full")
	}
}
