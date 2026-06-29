package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/butialabs/proxywi/internal/storage"
	"github.com/butialabs/proxywi/internal/tunnel"
	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
	"golang.org/x/crypto/bcrypt"
)

type Control struct {
	Store    *storage.Store
	Registry *Registry
	Log      *slog.Logger
	Hub      *Hub
	OnEvent  func(clientID int64, msg tunnel.MetaMessage)
}

func (c *Control) Handler() http.Handler {
	return http.HandlerFunc(c.serve)
}

func (c *Control) serve(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // host check disabled; origin verified below
	})
	if err != nil {
		c.Log.Warn("ws accept", "err", err)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		expected := "https://" + r.Host
		if !strings.EqualFold(origin, expected) && !strings.EqualFold(origin, "http://"+r.Host) {
			c.Log.Warn("ws origin rejected", "origin", origin, "host", r.Host)
			_ = ws.Close(websocket.StatusPolicyViolation, "origin not allowed")
			return
		}
	}
	ws.SetReadLimit(1 << 20) // 1 MiB message limit

	ctx := r.Context()

	handshakeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	mt, raw, err := ws.Read(handshakeCtx)
	if err != nil {
		_ = ws.Close(websocket.StatusPolicyViolation, "missing handshake")
		return
	}
	if mt != websocket.MessageText {
		_ = ws.Close(websocket.StatusPolicyViolation, "handshake must be text")
		return
	}
	var hs tunnel.Handshake
	if err := json.Unmarshal(raw, &hs); err != nil {
		_ = ws.Close(websocket.StatusPolicyViolation, "bad handshake json")
		return
	}
	if err := tunnel.ValidateHandshake(&hs); err != nil {
		_ = ws.Close(websocket.StatusPolicyViolation, err.Error())
		return
	}

	clientID, dbClient, err := c.authenticateToken(ctx, hs.Token)
	if err != nil {
		c.Log.Warn("handshake auth failed", "err", err)
		_ = writeAck(ctx, ws, tunnel.HandshakeAck{OK: false, Error: "invalid token"})
		_ = ws.Close(websocket.StatusPolicyViolation, "auth failed")
		return
	}

	if err := writeAck(ctx, ws, tunnel.HandshakeAck{OK: true, ClientID: clientID}); err != nil {
		_ = ws.Close(websocket.StatusInternalError, "ack write failed")
		return
	}

	netConn := websocket.NetConn(context.Background(), ws, websocket.MessageBinary)
	defer func() { _ = netConn.Close() }()

	yCfg := yamux.DefaultConfig()
	yCfg.EnableKeepAlive = true
	yCfg.KeepAliveInterval = 20 * time.Second
	yCfg.LogOutput = io.Discard

	session, err := yamux.Server(netConn, yCfg)
	if err != nil {
		c.Log.Warn("yamux server", "err", err)
		return
	}
	defer func() { _ = session.Close() }()

	agent := &Agent{
		ID:           clientID,
		Name:         dbClient.Name,
		AgentVersion: hs.AgentVersion,
		ConnectAt:    time.Now(),
		session:      session,
	}
	c.Registry.Add(agent)
	defer c.Registry.Remove(clientID, agent)

	if err := c.Store.MarkClientSeen(ctx, clientID, hs.AgentVersion); err != nil {
		c.Log.Warn("mark client seen", "err", err)
	}

	c.Log.Info("agent connected", "id", clientID, "name", dbClient.Name)
	if c.Hub != nil {
		c.Hub.Publish(Event{Type: "client_online", Data: ClientEvent{
			ID: clientID, Name: dbClient.Name,
		}})
	}

	go c.readMetaStream(ctx, agent)

	<-session.CloseChan()
	c.Log.Info("agent disconnected", "id", clientID, "name", dbClient.Name)
	if c.Hub != nil {
		c.Hub.Publish(Event{Type: "client_offline", Data: ClientEvent{
			ID: clientID, Name: dbClient.Name,
		}})
	}
}

func (c *Control) readMetaStream(ctx context.Context, agent *Agent) {
	stream, err := agent.session.Accept()
	if err != nil {
		return
	}
	defer func() { _ = stream.Close() }()

	br := bufio.NewReader(stream)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	go func() {
		for range ticker.C {
			_ = c.Store.MarkClientSeen(ctx, agent.ID, agent.AgentVersion)
		}
	}()

	for {
		var msg tunnel.MetaMessage
		if err := tunnel.ReadJSONLine(br, &msg); err != nil {
			return
		}
		if c.OnEvent != nil {
			c.OnEvent(agent.ID, msg)
		}
		if c.Hub != nil && msg.Type == "metrics" {
			c.Hub.Publish(Event{Type: "metrics", Data: MetricsEvent{
				ClientID: agent.ID,
				BytesIn:  msg.BytesIn,
				BytesOut: msg.BytesOut,
			}})
		}
	}
}

func (c *Control) authenticateToken(ctx context.Context, token string) (int64, *storage.Client, error) {
	tokenID := storage.TokenIDFromToken(token)
	if tokenID != "" {
		cl, err := c.Store.ClientByTokenID(ctx, tokenID)
		if err != nil {
			return 0, nil, err
		}
		if cl != nil && bcrypt.CompareHashAndPassword([]byte(cl.TokenHash), []byte(token)) == nil {
			return cl.ID, cl, nil
		}
	}
	// Fallback for legacy tokens without token_id.
	hashes, err := c.Store.AllClientTokenHashes(ctx)
	if err != nil {
		return 0, nil, err
	}
	for id, h := range hashes {
		if bcrypt.CompareHashAndPassword([]byte(h), []byte(token)) == nil {
			cl, err := c.Store.ClientByID(ctx, id)
			if err != nil || cl == nil {
				return 0, nil, errors.New("client vanished")
			}
			return id, cl, nil
		}
	}
	return 0, nil, errors.New("no matching token")
}

func writeAck(ctx context.Context, ws *websocket.Conn, ack tunnel.HandshakeAck) error {
	b, _ := json.Marshal(ack)
	return ws.Write(ctx, websocket.MessageText, b)
}
