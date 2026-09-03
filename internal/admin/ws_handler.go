package admin

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// WebSocketHandler manages real-time streaming connections for orchestration commands.
type WebSocketHandler struct {
	orchestrator *Orchestrator
}

// NewWebSocketHandler creates a new WebSocketHandler.
func NewWebSocketHandler(orchestrator *Orchestrator) *WebSocketHandler {
	return &WebSocketHandler{
		orchestrator: orchestrator,
	}
}

// ServeHTTP handles incoming WebSocket client connections at /ws/actions.
func (h *WebSocketHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		log.Printf("[ADMIN WS] WebSocket accept error: %v", err)
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "session closed")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	var writeMu sync.Mutex
	sendEvent := func(event ActionEvent) {
		data, err := json.Marshal(event)
		if err != nil {
			return
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		writeCtx, writeCancel := context.WithTimeout(ctx, 5*time.Second)
		defer writeCancel()
		_ = conn.Write(writeCtx, websocket.MessageText, data)
	}

	// Read loop: client sends ActionRequest JSON to trigger command execution
	for {
		msgType, msgData, err := conn.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure ||
				websocket.CloseStatus(err) == websocket.StatusGoingAway {
				return
			}
			log.Printf("[ADMIN WS] WebSocket read error / client disconnect: %v", err)
			return
		}

		if msgType != websocket.MessageText {
			continue
		}

		var req ActionRequest
		if err := json.Unmarshal(msgData, &req); err != nil {
			sendEvent(ActionEvent{
				Type:  "error",
				Error: "Invalid ActionRequest JSON: " + err.Error(),
			})
			continue
		}

		// Execute action in a goroutine so multiple commands or pings can be processed
		go func(actionReq ActionRequest) {
			_, execErr := h.orchestrator.Execute(ctx, actionReq, sendEvent)
			if execErr != nil {
				sendEvent(ActionEvent{
					Type:  "error",
					Error: execErr.Error(),
				})
			}
		}(req)
	}
}
