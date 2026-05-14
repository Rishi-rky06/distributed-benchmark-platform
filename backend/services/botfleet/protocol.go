package botfleet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ProtocolAdapter defines how bots communicate with the contestant's engine.
type ProtocolAdapter interface {
	Connect(ctx context.Context, target string) error
	SendOrder(ctx context.Context, order Order) (Ack, error)
	Close() error
}

// Ack is the acknowledgment from the contestant's engine.
type Ack struct {
	OrderID   string  `json:"order_id"`
	Status    string  `json:"status"` // "accepted" | "filled" | "rejected"
	FillPrice float64 `json:"fill_price,omitempty"`
	FillQty   int     `json:"fill_qty,omitempty"`
}

// NewAdapter creates a ProtocolAdapter for the given protocol.
func NewAdapter(protocol string) (ProtocolAdapter, error) {
	switch strings.ToLower(protocol) {
	case "rest", "http":
		return &RESTAdapter{}, nil
	case "websocket", "ws":
		return &WebSocketAdapter{}, nil
	case "fix":
		return &RESTAdapter{}, nil // TODO: implement FIX adapter
	default:
		return nil, fmt.Errorf("unsupported protocol: %s", protocol)
	}
}

// ── REST Adapter ─────────────────────────────────────────────────────────────

// RESTAdapter sends orders via HTTP POST.
type RESTAdapter struct {
	client  *http.Client
	baseURL string
}

func (a *RESTAdapter) Connect(ctx context.Context, target string) error {
	a.baseURL = fmt.Sprintf("http://%s", target)
	a.client = &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
			IdleConnTimeout:     90 * time.Second,
		},
	}
	return nil
}

func (a *RESTAdapter) SendOrder(ctx context.Context, order Order) (Ack, error) {
	body, _ := json.Marshal(order)
	req, err := http.NewRequestWithContext(ctx, "POST", a.baseURL+"/order", strings.NewReader(string(body)))
	if err != nil {
		return Ack{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return Ack{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return Ack{}, fmt.Errorf("order rejected: %d %s", resp.StatusCode, string(respBody))
	}

	var ack Ack
	if err := json.NewDecoder(resp.Body).Decode(&ack); err != nil {
		return Ack{}, fmt.Errorf("decode ack: %w", err)
	}
	return ack, nil
}

func (a *RESTAdapter) Close() error {
	if a.client != nil {
		a.client.CloseIdleConnections()
	}
	return nil
}

// ── WebSocket Adapter ────────────────────────────────────────────────────────

// WebSocketAdapter sends orders via a persistent WebSocket connection.
// Each bot worker gets its own adapter instance with a dedicated connection.
type WebSocketAdapter struct {
	conn   *websocket.Conn
	mu     sync.Mutex // serialize writes; reads are single-goroutine
	target string
}

func (a *WebSocketAdapter) Connect(ctx context.Context, target string) error {
	a.target = target
	url := fmt.Sprintf("ws://%s/ws", target)

	dialer := websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
	}

	conn, _, err := dialer.DialContext(ctx, url, nil)
	if err != nil {
		return fmt.Errorf("ws dial %s: %w", url, err)
	}

	a.conn = conn

	// Set sensible read deadline handling
	a.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	a.conn.SetPongHandler(func(string) error {
		a.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		return nil
	})

	return nil
}

func (a *WebSocketAdapter) SendOrder(ctx context.Context, order Order) (Ack, error) {
	if a.conn == nil {
		return Ack{}, fmt.Errorf("ws not connected")
	}

	data, err := json.Marshal(order)
	if err != nil {
		return Ack{}, fmt.Errorf("marshal order: %w", err)
	}

	// Serialize writes — WebSocket connections are not concurrent-write-safe
	a.mu.Lock()
	err = a.conn.WriteMessage(websocket.TextMessage, data)
	a.mu.Unlock()
	if err != nil {
		return Ack{}, fmt.Errorf("ws write: %w", err)
	}

	// Read acknowledgment — set deadline per-message
	a.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := a.conn.ReadMessage()
	if err != nil {
		return Ack{}, fmt.Errorf("ws read ack: %w", err)
	}

	var ack Ack
	if err := json.Unmarshal(msg, &ack); err != nil {
		return Ack{}, fmt.Errorf("decode ws ack: %w", err)
	}

	return ack, nil
}

func (a *WebSocketAdapter) Close() error {
	if a.conn != nil {
		// Send close frame gracefully
		_ = a.conn.WriteMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		)
		return a.conn.Close()
	}
	return nil
}
