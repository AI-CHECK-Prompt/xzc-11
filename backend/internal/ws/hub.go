package ws

import (
	"encoding/json"
	"log"
	"sync"
	"time"
	"tunnel-shm/internal/model"

	"github.com/gorilla/websocket"
)

// Message WebSocket消息
type Message struct {
	Type      string                   `json:"type"` // "data_update", "alert", "heartbeat"
	Data      interface{}              `json:"data"`
	Timestamp time.Time               `json:"timestamp"`
}

// Client WebSocket客户端
type Client struct {
	conn     *websocket.Conn
	hub      *Hub
	send     chan []byte
	closed   bool
	mu       sync.Mutex
}

// Hub WebSocket中心
type Hub struct {
	clients    map[*Client]bool
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
	mu         sync.RWMutex
	OnAlertDataFunc func(data *model.SectionRealtimeData)
}

// NewHub 创建Hub
func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *Client),
		unregister: make(chan *Client),
	}
}

// Run 启动Hub
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			count := len(h.clients)
			h.mu.Unlock()
			log.Printf("【WS-连接】新客户端接入，当前连接数: %d", count)

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				client.mu.Lock()
				if !client.closed {
					client.closed = true
					close(client.send)
					client.conn.Close()
				}
				client.mu.Unlock()
				delete(h.clients, client)
			}
			count := len(h.clients)
			h.mu.Unlock()
			log.Printf("【WS-连接】客户端断开，当前连接数: %d", count)

		case message := <-h.broadcast:
			h.mu.RLock()
			clients := make([]*Client, 0, len(h.clients))
			for client := range h.clients {
				clients = append(clients, client)
			}
			h.mu.RUnlock()

			for _, client := range clients {
				client.mu.Lock()
				closed := client.closed
				client.mu.Unlock()
				if closed {
					continue
				}
				select {
				case client.send <- message:
				default:
					h.unregister <- client
				}
			}
		}
	}
}

// BroadcastDataUpdate 广播数据更新
func (h *Hub) BroadcastDataUpdate(data *model.SectionRealtimeData) {
	msg := Message{
		Type:      "data_update",
		Data:      data,
		Timestamp: time.Now(),
	}
	jsonData, err := json.Marshal(msg)
	if err != nil {
		log.Printf("【WS-错误】序列化消息失败: %v", err)
		return
	}
	h.broadcast <- jsonData
}

// BroadcastAlert 广播告警
func (h *Hub) BroadcastAlert(alert *model.Alert) {
	msg := Message{
		Type:      "alert",
		Data:      alert,
		Timestamp: time.Now(),
	}
	jsonData, err := json.Marshal(msg)
	if err != nil {
		log.Printf("【WS-错误】序列化告警消息失败: %v", err)
		return
	}
	h.broadcast <- jsonData
}

// NewClient 创建客户端
func NewClient(conn *websocket.Conn, hub *Hub) *Client {
	return &Client{
		conn: conn,
		hub:  hub,
		send: make(chan []byte, 256),
	}
}

// Register 注册客户端到Hub
func (h *Hub) Register(client *Client) {
	h.register <- client
}

// ClientCount 获取当前客户端数
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// WritePump 客户端写协程
func (c *Client) WritePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.mu.Lock()
		if !c.closed {
			c.closed = true
			c.conn.Close()
		}
		c.mu.Unlock()
	}()

	for {
		select {
		case message, ok := <-c.send:
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// ReadPump 客户端读协程
func (c *Client) ReadPump(hub *Hub) {
	defer func() {
		hub.unregister <- c
	}()

	c.conn.SetReadLimit(4096)
	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("【WS-错误】连接异常关闭: %v", err)
			}
			break
		}
	}
}