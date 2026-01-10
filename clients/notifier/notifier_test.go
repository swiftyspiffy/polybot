package notifier

import (
	"errors"
	"testing"
	"time"
)

// mockNotifier is a test helper that implements Notifier interface
type mockNotifier struct {
	alerts    []TradeAlert
	closeErr  error
	closeCalled bool
}

func (m *mockNotifier) SendTradeAlert(alert TradeAlert) {
	m.alerts = append(m.alerts, alert)
}

func (m *mockNotifier) Close() error {
	m.closeCalled = true
	return m.closeErr
}

func TestNewMultiNotifier_FiltersNil(t *testing.T) {
	mock1 := &mockNotifier{}
	mock2 := &mockNotifier{}

	mn := NewMultiNotifier(mock1, nil, mock2, nil)

	if mn.Count() != 2 {
		t.Errorf("expected 2 notifiers, got %d", mn.Count())
	}
}

func TestNewMultiNotifier_AllNil(t *testing.T) {
	mn := NewMultiNotifier(nil, nil, nil)

	if mn.Count() != 0 {
		t.Errorf("expected 0 notifiers, got %d", mn.Count())
	}
}

func TestNewMultiNotifier_Empty(t *testing.T) {
	mn := NewMultiNotifier()

	if mn.Count() != 0 {
		t.Errorf("expected 0 notifiers, got %d", mn.Count())
	}
}

func TestMultiNotifier_SendTradeAlert(t *testing.T) {
	mock1 := &mockNotifier{}
	mock2 := &mockNotifier{}

	mn := NewMultiNotifier(mock1, mock2)

	alert := TradeAlert{
		TraderName:  "TestTrader",
		Side:        "BUY",
		Shares:      100,
		Price:       0.5,
		Notional:    50,
		MarketTitle: "Test Market",
	}

	mn.SendTradeAlert(alert)

	if len(mock1.alerts) != 1 {
		t.Errorf("expected 1 alert for mock1, got %d", len(mock1.alerts))
	}
	if len(mock2.alerts) != 1 {
		t.Errorf("expected 1 alert for mock2, got %d", len(mock2.alerts))
	}
	if mock1.alerts[0].TraderName != "TestTrader" {
		t.Errorf("expected TraderName 'TestTrader', got %s", mock1.alerts[0].TraderName)
	}
}

func TestMultiNotifier_SendTradeAlert_NoNotifiers(t *testing.T) {
	mn := NewMultiNotifier()

	alert := TradeAlert{TraderName: "Test"}

	// Should not panic
	mn.SendTradeAlert(alert)
}

func TestMultiNotifier_Close_Success(t *testing.T) {
	mock1 := &mockNotifier{}
	mock2 := &mockNotifier{}

	mn := NewMultiNotifier(mock1, mock2)

	err := mn.Close()

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !mock1.closeCalled {
		t.Error("expected mock1.Close() to be called")
	}
	if !mock2.closeCalled {
		t.Error("expected mock2.Close() to be called")
	}
}

func TestMultiNotifier_Close_WithError(t *testing.T) {
	expectedErr := errors.New("close error")
	mock1 := &mockNotifier{closeErr: expectedErr}
	mock2 := &mockNotifier{}

	mn := NewMultiNotifier(mock1, mock2)

	err := mn.Close()

	if err != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
	// Both should still be called
	if !mock1.closeCalled {
		t.Error("expected mock1.Close() to be called")
	}
	if !mock2.closeCalled {
		t.Error("expected mock2.Close() to be called")
	}
}

func TestMultiNotifier_Close_MultipleErrors(t *testing.T) {
	err1 := errors.New("error 1")
	err2 := errors.New("error 2")
	mock1 := &mockNotifier{closeErr: err1}
	mock2 := &mockNotifier{closeErr: err2}

	mn := NewMultiNotifier(mock1, mock2)

	err := mn.Close()

	// Should return the last error
	if err != err2 {
		t.Errorf("expected last error %v, got %v", err2, err)
	}
}

func TestMultiNotifier_Close_Empty(t *testing.T) {
	mn := NewMultiNotifier()

	err := mn.Close()

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMultiNotifier_Count(t *testing.T) {
	tests := []struct {
		name     string
		notifiers []Notifier
		expected int
	}{
		{"empty", []Notifier{}, 0},
		{"one", []Notifier{&mockNotifier{}}, 1},
		{"three", []Notifier{&mockNotifier{}, &mockNotifier{}, &mockNotifier{}}, 3},
		{"with nils", []Notifier{&mockNotifier{}, nil, &mockNotifier{}}, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mn := NewMultiNotifier(tt.notifiers...)
			if mn.Count() != tt.expected {
				t.Errorf("expected %d, got %d", tt.expected, mn.Count())
			}
		})
	}
}

func TestTradeAlert_AllFields(t *testing.T) {
	ts := time.Now()
	alert := TradeAlert{
		TraderName:    "TestTrader",
		TraderAddress: "0x123",
		WalletURL:     "https://example.com/wallet",
		Side:          "BUY",
		Shares:        100.5,
		Price:         0.75,
		Notional:      75.375,
		MarketTitle:   "Test Market",
		MarketURL:     "https://example.com/market",
		MarketImage:   "https://example.com/image.png",
		Outcome:       "Yes",
		UniqueMarkets: 5,
		WinRate:       0.65,
		WinCount:      13,
		LossCount:     7,
		Reasons:       []AlertReason{AlertReasonLowActivity, AlertReasonHighWinRate},
		Timestamp:     ts,
	}

	// Verify all fields
	if alert.TraderName != "TestTrader" {
		t.Error("TraderName mismatch")
	}
	if alert.TraderAddress != "0x123" {
		t.Error("TraderAddress mismatch")
	}
	if alert.WalletURL != "https://example.com/wallet" {
		t.Error("WalletURL mismatch")
	}
	if alert.Side != "BUY" {
		t.Error("Side mismatch")
	}
	if alert.Shares != 100.5 {
		t.Error("Shares mismatch")
	}
	if alert.Price != 0.75 {
		t.Error("Price mismatch")
	}
	if alert.Notional != 75.375 {
		t.Error("Notional mismatch")
	}
	if alert.MarketTitle != "Test Market" {
		t.Error("MarketTitle mismatch")
	}
	if alert.MarketURL != "https://example.com/market" {
		t.Error("MarketURL mismatch")
	}
	if alert.MarketImage != "https://example.com/image.png" {
		t.Error("MarketImage mismatch")
	}
	if alert.Outcome != "Yes" {
		t.Error("Outcome mismatch")
	}
	if alert.UniqueMarkets != 5 {
		t.Error("UniqueMarkets mismatch")
	}
	if alert.WinRate != 0.65 {
		t.Error("WinRate mismatch")
	}
	if alert.WinCount != 13 {
		t.Error("WinCount mismatch")
	}
	if alert.LossCount != 7 {
		t.Error("LossCount mismatch")
	}
	if len(alert.Reasons) != 2 {
		t.Error("Reasons length mismatch")
	}
	if alert.Timestamp != ts {
		t.Error("Timestamp mismatch")
	}
}

func TestAlertReason_Values(t *testing.T) {
	tests := []struct {
		reason   AlertReason
		expected string
	}{
		{AlertReasonLowActivity, "low_activity"},
		{AlertReasonHighWinRate, "high_win_rate"},
		{AlertReasonExtremeBet, "extreme_bet"},
		{AlertReasonRapidTrading, "rapid_trading"},
		{AlertReasonNewWallet, "new_wallet"},
		{AlertReasonContrarianBet, "contrarian_bet"},
		{AlertReasonMassiveTrade, "massive_trade"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if string(tt.reason) != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, string(tt.reason))
			}
		})
	}
}
