package polymarketevents

import (
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestNewPolymarketEventsClient(t *testing.T) {
	client := NewPolymarketEventsClient(nil)

	if client.logger == nil {
		t.Error("expected logger to be set")
	}
	if client.marketWSURL != "wss://ws-subscriptions-clob.polymarket.com/ws/market" {
		t.Errorf("unexpected WS URL: %s", client.marketWSURL)
	}
	if client.pingInterval != 10*time.Second {
		t.Errorf("unexpected ping interval: %v", client.pingInterval)
	}
	if !client.customFeatureEnabled {
		t.Error("expected custom feature to be enabled")
	}
	if client.msgCh == nil {
		t.Error("expected msgCh to be initialized")
	}
	if client.errCh == nil {
		t.Error("expected errCh to be initialized")
	}
	if client.closeCh == nil {
		t.Error("expected closeCh to be initialized")
	}
}

func TestNewPolymarketEventsClient_WithLogger(t *testing.T) {
	logger := zap.NewNop()
	client := NewPolymarketEventsClient(logger)

	if client.logger != logger {
		t.Error("expected custom logger to be set")
	}
}

func TestMessages(t *testing.T) {
	client := NewPolymarketEventsClient(nil)

	ch := client.Messages()
	if ch == nil {
		t.Error("expected non-nil channel")
	}
}

func TestErrors(t *testing.T) {
	client := NewPolymarketEventsClient(nil)

	ch := client.Errors()
	if ch == nil {
		t.Error("expected non-nil channel")
	}
}

func TestStats_Empty(t *testing.T) {
	client := NewPolymarketEventsClient(nil)

	stats := client.Stats()

	if stats.MessageCount != 0 {
		t.Errorf("expected 0 messages, got %d", stats.MessageCount)
	}
	if !stats.LastMessageAt.IsZero() {
		t.Error("expected zero time for last message")
	}
}

func TestClose_NoConnection(t *testing.T) {
	client := NewPolymarketEventsClient(nil)

	err := client.Close()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Second close should also be safe
	err = client.Close()
	if err != nil {
		t.Errorf("unexpected error on second close: %v", err)
	}
}

func TestSubscribeAssets_NotConnected(t *testing.T) {
	client := NewPolymarketEventsClient(nil)

	err := client.SubscribeAssets([]string{"asset1", "asset2"})
	if err == nil {
		t.Error("expected error when not connected")
	}
}

func TestUnsubscribeAssets_NotConnected(t *testing.T) {
	client := NewPolymarketEventsClient(nil)

	err := client.UnsubscribeAssets([]string{"asset1"})
	if err == nil {
		t.Error("expected error when not connected")
	}
}

func TestWSStats(t *testing.T) {
	now := time.Now()
	stats := WSStats{
		MessageCount:  100,
		LastMessageAt: now,
	}

	if stats.MessageCount != 100 {
		t.Errorf("unexpected message count: %d", stats.MessageCount)
	}
	if stats.LastMessageAt != now {
		t.Error("unexpected last message time")
	}
}

func TestStats_WithMessages(t *testing.T) {
	client := NewPolymarketEventsClient(nil)

	// Simulate having received messages by setting atomic values
	// We can't easily test this without actually receiving messages
	// but we can test the stats structure

	stats := client.Stats()

	// Initially should be zero
	if stats.MessageCount != 0 {
		t.Errorf("expected 0, got %d", stats.MessageCount)
	}
}

func TestEmitFrame_EmptyInput(t *testing.T) {
	client := NewPolymarketEventsClient(nil)

	// Should not panic or block
	client.emitFrame([]byte{})
	client.emitFrame([]byte("   \n\t\r  "))
}

func TestEmitFrame_SingleObject(t *testing.T) {
	client := NewPolymarketEventsClient(nil)

	go func() {
		client.emitFrame([]byte(`{"event": "test"}`))
	}()

	select {
	case msg := <-client.msgCh:
		if string(msg) != `{"event": "test"}` {
			t.Errorf("unexpected message: %s", string(msg))
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("expected message to be forwarded")
	}
}

func TestEmitFrame_Array(t *testing.T) {
	client := NewPolymarketEventsClient(nil)

	go func() {
		client.emitFrame([]byte(`[{"event": "a"}, {"event": "b"}]`))
	}()

	// Should receive both messages
	received := 0
	for i := 0; i < 2; i++ {
		select {
		case <-client.msgCh:
			received++
		case <-time.After(100 * time.Millisecond):
			t.Error("expected message to be forwarded")
		}
	}

	if received != 2 {
		t.Errorf("expected 2 messages, got %d", received)
	}
}

func TestEmitFrame_EmptyArray(t *testing.T) {
	client := NewPolymarketEventsClient(zap.NewNop())

	// Should not forward anything
	client.emitFrame([]byte(`[]`))

	select {
	case <-client.msgCh:
		t.Error("should not forward empty array")
	case <-time.After(50 * time.Millisecond):
		// Good
	}
}

func TestEmitFrame_InvalidJSON(t *testing.T) {
	client := NewPolymarketEventsClient(zap.NewNop())

	// Should not panic
	client.emitFrame([]byte(`[not valid json`))
}

func TestForward_ChannelFull(t *testing.T) {
	client := NewPolymarketEventsClient(zap.NewNop())

	// Fill the channel
	for i := 0; i < 1024; i++ {
		select {
		case client.msgCh <- []byte(`{"i": 0}`):
		default:
			break
		}
	}

	// Should not block when channel is full
	done := make(chan struct{})
	go func() {
		client.forward([]byte(`{"overflow": true}`))
		close(done)
	}()

	select {
	case <-done:
		// Good, didn't block
	case <-time.After(100 * time.Millisecond):
		t.Error("forward should not block when channel is full")
	}
}

func TestWriteJSON_NotConnected(t *testing.T) {
	client := NewPolymarketEventsClient(nil)

	err := client.writeJSON(map[string]string{"test": "value"})
	if err == nil {
		t.Error("expected error when not connected")
	}
}

func TestSendOp_NotConnected(t *testing.T) {
	client := NewPolymarketEventsClient(nil)

	err := client.sendOp("subscribe", []string{"asset1"})
	if err == nil {
		t.Error("expected error when not connected")
	}
}

func TestConnectMarket_AlreadyConnected(t *testing.T) {
	// We can't easily test the full connection, but we can test the error
	// path by manually setting conn to non-nil
	_ = NewPolymarketEventsClient(nil)

	// We can't set conn directly since it's unexported, but we tested
	// the not-connected paths above
}

func TestEmitFrame_WhitespaceVariants(t *testing.T) {
	client := NewPolymarketEventsClient(nil)

	// Test various whitespace prefixes
	go func() {
		client.emitFrame([]byte(`  {"event": "test"}`))
	}()

	select {
	case msg := <-client.msgCh:
		if string(msg) != `{"event": "test"}` {
			t.Errorf("unexpected message: %s", string(msg))
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("expected message")
	}
}

func TestEmitFrame_TabPrefix(t *testing.T) {
	client := NewPolymarketEventsClient(nil)

	go func() {
		client.emitFrame([]byte("\t\n\r{\"key\": \"val\"}"))
	}()

	select {
	case msg := <-client.msgCh:
		if len(msg) == 0 {
			t.Error("expected non-empty message")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("expected message")
	}
}

func TestNewPolymarketEventsClient_ChannelBuffers(t *testing.T) {
	client := NewPolymarketEventsClient(nil)

	// Check buffer sizes by attempting to fill them
	// msgCh should have buffer of 1024
	for i := 0; i < 1024; i++ {
		select {
		case client.msgCh <- []byte(`{}`):
		default:
			t.Errorf("msgCh buffer smaller than expected, only fit %d", i)
			return
		}
	}

	// Should be full now
	select {
	case client.msgCh <- []byte(`{}`):
		t.Error("expected msgCh to be full")
	default:
		// Good
	}
}

func TestParseTradeEvent_ValidTrade(t *testing.T) {
	data := []byte(`{
		"event_type": "trade",
		"asset_id": "abc123",
		"price": "0.75",
		"size": "100.5",
		"side": "BUY",
		"maker_address": "0xmaker",
		"taker_address": "0xtaker",
		"timestamp": "1704067200",
		"transaction_hash": "0xtxhash",
		"fee_rate_bps": "10",
		"id": "trade123"
	}`)

	event := ParseTradeEvent(data)

	if event == nil {
		t.Fatal("expected non-nil event")
	}
	if event.EventType != "trade" {
		t.Errorf("expected event_type 'trade', got %s", event.EventType)
	}
	if event.AssetID != "abc123" {
		t.Errorf("expected asset_id 'abc123', got %s", event.AssetID)
	}
	if event.Price != "0.75" {
		t.Errorf("expected price '0.75', got %s", event.Price)
	}
	if event.Size != "100.5" {
		t.Errorf("expected size '100.5', got %s", event.Size)
	}
	if event.Side != "BUY" {
		t.Errorf("expected side 'BUY', got %s", event.Side)
	}
	if event.MakerAddress != "0xmaker" {
		t.Errorf("expected maker_address '0xmaker', got %s", event.MakerAddress)
	}
	if event.TakerAddress != "0xtaker" {
		t.Errorf("expected taker_address '0xtaker', got %s", event.TakerAddress)
	}
	if event.TransactionHash != "0xtxhash" {
		t.Errorf("expected transaction_hash '0xtxhash', got %s", event.TransactionHash)
	}
}

func TestParseTradeEvent_LastTradePrice(t *testing.T) {
	data := []byte(`{"event_type": "last_trade_price", "price": "0.50"}`)

	event := ParseTradeEvent(data)

	if event == nil {
		t.Fatal("expected non-nil event for last_trade_price")
	}
	if event.EventType != "last_trade_price" {
		t.Errorf("expected event_type 'last_trade_price', got %s", event.EventType)
	}
}

func TestParseTradeEvent_NonTradeEvent(t *testing.T) {
	data := []byte(`{"event_type": "price_change", "price": "0.50"}`)

	event := ParseTradeEvent(data)

	if event != nil {
		t.Error("expected nil for non-trade event")
	}
}

func TestParseTradeEvent_InvalidJSON(t *testing.T) {
	data := []byte(`not valid json`)

	event := ParseTradeEvent(data)

	if event != nil {
		t.Error("expected nil for invalid JSON")
	}
}

func TestParseTradeEvent_EmptyEventType(t *testing.T) {
	data := []byte(`{"price": "0.50"}`)

	event := ParseTradeEvent(data)

	if event != nil {
		t.Error("expected nil when event_type is missing")
	}
}

func TestParseEventType_Valid(t *testing.T) {
	data := []byte(`{"event_type": "trade"}`)

	eventType := ParseEventType(data)

	if eventType != "trade" {
		t.Errorf("expected 'trade', got %s", eventType)
	}
}

func TestParseEventType_Empty(t *testing.T) {
	data := []byte(`{"price": "0.50"}`)

	eventType := ParseEventType(data)

	if eventType != "empty" {
		t.Errorf("expected 'empty', got %s", eventType)
	}
}

func TestParseEventType_InvalidJSON(t *testing.T) {
	data := []byte(`not valid`)

	eventType := ParseEventType(data)

	if eventType != "unknown" {
		t.Errorf("expected 'unknown', got %s", eventType)
	}
}

func TestTradeEvent_GetPriceFloat(t *testing.T) {
	tests := []struct {
		price    string
		expected float64
	}{
		{"0.75", 0.75},
		{"1.0", 1.0},
		{"0.001", 0.001},
		{"", 0},
		{"invalid", 0},
	}

	for _, tt := range tests {
		t.Run(tt.price, func(t *testing.T) {
			event := &TradeEvent{Price: tt.price}
			result := event.GetPriceFloat()
			if result != tt.expected {
				t.Errorf("GetPriceFloat(%s) = %f, want %f", tt.price, result, tt.expected)
			}
		})
	}
}

func TestTradeEvent_GetSizeFloat(t *testing.T) {
	tests := []struct {
		size     string
		expected float64
	}{
		{"100.5", 100.5},
		{"1000", 1000},
		{"0.001", 0.001},
		{"", 0},
		{"invalid", 0},
	}

	for _, tt := range tests {
		t.Run(tt.size, func(t *testing.T) {
			event := &TradeEvent{Size: tt.size}
			result := event.GetSizeFloat()
			if result != tt.expected {
				t.Errorf("GetSizeFloat(%s) = %f, want %f", tt.size, result, tt.expected)
			}
		})
	}
}

func TestTradeEvent_GetTimestampUnix(t *testing.T) {
	tests := []struct {
		timestamp string
		expected  int64
	}{
		{"1704067200", 1704067200},
		{"0", 0},
		{"", 0},
		{"invalid", 0},
	}

	for _, tt := range tests {
		t.Run(tt.timestamp, func(t *testing.T) {
			event := &TradeEvent{Timestamp: tt.timestamp}
			result := event.GetTimestampUnix()
			if result != tt.expected {
				t.Errorf("GetTimestampUnix(%s) = %d, want %d", tt.timestamp, result, tt.expected)
			}
		})
	}
}

func TestTradeEvent_AllFields(t *testing.T) {
	event := TradeEvent{
		EventType:       "trade",
		AssetID:         "asset123",
		Price:           "0.75",
		Size:            "100",
		Side:            "BUY",
		MakerAddress:    "0xmaker",
		TakerAddress:    "0xtaker",
		Timestamp:       "1704067200",
		TransactionHash: "0xtxhash",
		FeeRateBps:      "10",
		TradeID:         "trade123",
	}

	// Verify all fields
	if event.EventType != "trade" {
		t.Error("EventType mismatch")
	}
	if event.AssetID != "asset123" {
		t.Error("AssetID mismatch")
	}
	if event.FeeRateBps != "10" {
		t.Error("FeeRateBps mismatch")
	}
	if event.TradeID != "trade123" {
		t.Error("TradeID mismatch")
	}
}

func TestClose_MultipleCloses(t *testing.T) {
	client := NewPolymarketEventsClient(nil)

	// Multiple closes should be safe
	for i := 0; i < 5; i++ {
		err := client.Close()
		if err != nil {
			t.Errorf("close %d returned error: %v", i, err)
		}
	}
}

func TestEmitFrame_ArrayWithWhitespace(t *testing.T) {
	client := NewPolymarketEventsClient(nil)

	go func() {
		client.emitFrame([]byte(`  [{"event": "a"}]`))
	}()

	select {
	case msg := <-client.msgCh:
		if string(msg) != `{"event": "a"}` {
			t.Errorf("unexpected message: %s", string(msg))
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("expected message")
	}
}

func TestEmitFrame_NestedArray(t *testing.T) {
	client := NewPolymarketEventsClient(nil)

	go func() {
		client.emitFrame([]byte(`[{"nested": {"value": 1}}, {"nested": {"value": 2}}]`))
	}()

	// Should receive both messages
	received := 0
	for i := 0; i < 2; i++ {
		select {
		case <-client.msgCh:
			received++
		case <-time.After(100 * time.Millisecond):
		}
	}

	if received != 2 {
		t.Errorf("expected 2 messages, got %d", received)
	}
}

func TestEmitFrame_LargeArray(t *testing.T) {
	client := NewPolymarketEventsClient(nil)

	// Create array with 10 elements
	data := []byte(`[{"i":0},{"i":1},{"i":2},{"i":3},{"i":4},{"i":5},{"i":6},{"i":7},{"i":8},{"i":9}]`)

	go func() {
		client.emitFrame(data)
	}()

	// Should receive all messages
	received := 0
	for i := 0; i < 10; i++ {
		select {
		case <-client.msgCh:
			received++
		case <-time.After(100 * time.Millisecond):
		}
	}

	if received != 10 {
		t.Errorf("expected 10 messages, got %d", received)
	}
}

func TestEmitFrame_OnlyWhitespace(t *testing.T) {
	client := NewPolymarketEventsClient(nil)

	// Should handle various whitespace-only inputs
	client.emitFrame([]byte(" "))
	client.emitFrame([]byte("\t"))
	client.emitFrame([]byte("\n"))
	client.emitFrame([]byte("\r"))
	client.emitFrame([]byte("  \n\t\r  "))

	// Nothing should be forwarded
	select {
	case <-client.msgCh:
		t.Error("should not forward whitespace-only frames")
	case <-time.After(50 * time.Millisecond):
		// Good
	}
}

func TestWSStats_ZeroTime(t *testing.T) {
	stats := WSStats{
		MessageCount:  0,
		LastMessageAt: time.Time{},
	}

	if !stats.LastMessageAt.IsZero() {
		t.Error("expected zero time")
	}
}

func TestParseTradeEvent_AllFieldsParsed(t *testing.T) {
	data := []byte(`{
		"event_type": "trade",
		"asset_id": "test-asset",
		"price": "0.55",
		"size": "250.75",
		"side": "SELL",
		"maker_address": "0x1234",
		"taker_address": "0x5678",
		"timestamp": "1700000000",
		"transaction_hash": "0xabcd",
		"fee_rate_bps": "5",
		"id": "trade-id-123"
	}`)

	event := ParseTradeEvent(data)
	if event == nil {
		t.Fatal("expected non-nil event")
	}

	// Verify helper functions
	if event.GetPriceFloat() != 0.55 {
		t.Errorf("expected price 0.55, got %f", event.GetPriceFloat())
	}
	if event.GetSizeFloat() != 250.75 {
		t.Errorf("expected size 250.75, got %f", event.GetSizeFloat())
	}
	if event.GetTimestampUnix() != 1700000000 {
		t.Errorf("expected timestamp 1700000000, got %d", event.GetTimestampUnix())
	}
}

func TestNewPolymarketEventsClient_DefaultDialer(t *testing.T) {
	client := NewPolymarketEventsClient(nil)

	if client.dialer == nil {
		t.Error("expected dialer to be set")
	}
}

func TestSendOp_CustomFeatureEnabled(t *testing.T) {
	client := NewPolymarketEventsClient(nil)

	// Custom feature is enabled by default
	if !client.customFeatureEnabled {
		t.Error("expected custom feature to be enabled")
	}

	// sendOp should fail when not connected
	err := client.sendOp("subscribe", []string{"asset1"})
	if err == nil {
		t.Error("expected error when not connected")
	}
}

func TestEmitFrame_MalformedArrayJSON(t *testing.T) {
	client := NewPolymarketEventsClient(zap.NewNop())

	// Should handle malformed array JSON gracefully
	client.emitFrame([]byte(`[{"incomplete": true`))

	// Nothing should be forwarded for malformed JSON
	select {
	case <-client.msgCh:
		t.Error("should not forward malformed JSON")
	case <-time.After(50 * time.Millisecond):
		// Good
	}
}

func TestForward_EmptyChannel(t *testing.T) {
	client := NewPolymarketEventsClient(nil)

	// Forward to empty channel
	client.forward([]byte(`{"test": true}`))

	select {
	case msg := <-client.msgCh:
		if string(msg) != `{"test": true}` {
			t.Errorf("unexpected message: %s", string(msg))
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("expected message")
	}
}

func TestClient_ChannelAccess(t *testing.T) {
	client := NewPolymarketEventsClient(nil)

	// Test that channels are accessible
	msgCh := client.Messages()
	errCh := client.Errors()

	if msgCh == nil {
		t.Error("Messages() returned nil")
	}
	if errCh == nil {
		t.Error("Errors() returned nil")
	}

	// Verify they're the same channels
	go func() {
		client.msgCh <- []byte(`{}`)
	}()

	select {
	case <-msgCh:
		// Good
	case <-time.After(100 * time.Millisecond):
		t.Error("expected message from Messages() channel")
	}
}
