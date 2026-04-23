package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/butialabs/proxywi/internal/tunnel"
	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

type Agent struct {
	ServerURL string
	Token     string
	Name      string
	Log       *slog.Logger
}

func (a *Agent) Run(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	ws, resp, err := websocket.Dial(dialCtx, a.ServerURL+"/ws/control", &websocket.DialOptions{})
	if err != nil {
		return fmt.Errorf("ws dial: %w", err)
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	ws.SetReadLimit(-1)

	defer ws.CloseNow()

	publicIP := lookupPublicIP(dialCtx, a.Log)
	if publicIP != "" {
		a.Log.Info("reporting self IP to server", "ip", publicIP)
	}
	if err := ws.Write(dialCtx, websocket.MessageText, mustJSON(tunnel.Handshake{
		Version:        tunnel.ProtocolVersion,
		Token:          a.Token,
		Name:           a.Name,
		SelfReportedIP: publicIP,
	})); err != nil {
		return fmt.Errorf("write handshake: %w", err)
	}

	mt, raw, err := ws.Read(dialCtx)
	if err != nil {
		return fmt.Errorf("read ack: %w", err)
	}
	if mt != websocket.MessageText {
		return errors.New("ack not text")
	}
	var ack tunnel.HandshakeAck
	if err := json.Unmarshal(raw, &ack); err != nil {
		return fmt.Errorf("decode ack: %w", err)
	}
	if !ack.OK {
		return fmt.Errorf("handshake rejected: %s", ack.Error)
	}
	a.Log.Info("tunnel established", "client_id", ack.ClientID, "server", a.ServerURL)

	netConn := websocket.NetConn(context.Background(), ws, websocket.MessageBinary)
	defer netConn.Close()

	yCfg := yamux.DefaultConfig()
	yCfg.EnableKeepAlive = true
	yCfg.KeepAliveInterval = 20 * time.Second
	yCfg.LogOutput = io.Discard

	session, err := yamux.Client(netConn, yCfg)
	if err != nil {
		return fmt.Errorf("yamux client: %w", err)
	}
	defer session.Close()

	metaStream, err := session.Open()
	if err != nil {
		return fmt.Errorf("open meta stream: %w", err)
	}
	defer metaStream.Close()

	var counters counters
	go a.metaLoop(ctx, metaStream, &counters)

	for {
		stream, err := session.Accept()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, yamux.ErrSessionShutdown) {
				return nil
			}
			return fmt.Errorf("accept: %w", err)
		}
		go a.handleStream(ctx, stream, &counters)
	}
}

type counters struct {
	bytesIn     atomic.Int64
	bytesOut    atomic.Int64
	activeConns atomic.Int64
}

func (a *Agent) handleStream(ctx context.Context, stream net.Conn, c *counters) {
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(10 * time.Second))

	br := bufio.NewReader(stream)
	var req tunnel.ProxyRequest
	if err := tunnel.ReadJSONLine(br, &req); err != nil {
		a.Log.Warn("read proxy request", "err", err)
		return
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	target, err := dialer.DialContext(ctx, "tcp", req.Target)
	if err != nil {
		_ = tunnel.WriteJSONLine(stream, tunnel.ProxyReply{OK: false, Error: err.Error()})
		return
	}
	defer target.Close()

	if err := tunnel.WriteJSONLine(stream, tunnel.ProxyReply{OK: true}); err != nil {
		return
	}
	_ = stream.SetDeadline(time.Time{})

	c.activeConns.Add(1)
	defer c.activeConns.Add(-1)

	buffered := br.Buffered()
	if buffered > 0 {
		buf := make([]byte, buffered)
		if _, err := io.ReadFull(br, buf); err == nil {
			_, _ = target.Write(buf)
			c.bytesOut.Add(int64(buffered))
		}
	}

	done := make(chan struct{}, 2)
	go func() {
		n, _ := copyCounted(target, stream)
		c.bytesOut.Add(n)
		_ = target.(*net.TCPConn).CloseWrite()
		done <- struct{}{}
	}()
	go func() {
		n, _ := copyCounted(stream, target)
		c.bytesIn.Add(n)
		done <- struct{}{}
	}()
	<-done
}

func (a *Agent) metaLoop(ctx context.Context, stream net.Conn, c *counters) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	_ = tunnel.WriteJSONLine(stream, tunnel.MetaMessage{Type: "heartbeat"})
	var lastIn, lastOut int64
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			in := c.bytesIn.Load()
			out := c.bytesOut.Load()
			msg := tunnel.MetaMessage{
				Type:        "metrics",
				BytesIn:     in - lastIn,
				BytesOut:    out - lastOut,
				ActiveConns: int(c.activeConns.Load()),
			}
			lastIn, lastOut = in, out
			if err := tunnel.WriteJSONLine(stream, msg); err != nil {
				return
			}
		}
	}
}

func copyCounted(dst io.Writer, src io.Reader) (int64, error) {
	return io.Copy(dst, src)
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func lookupPublicIP(ctx context.Context, log *slog.Logger) string {
	endpoints := []string{"https://ifconfig.me/ip", "https://icanhazip.com"}
	client := &http.Client{Timeout: 5 * time.Second}
	for _, endpoint := range endpoints {
		reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
		if err != nil {
			cancel()
			continue
		}
		req.Header.Set("User-Agent", "curl/proxywi")
		resp, err := client.Do(req)
		cancel()
		if err != nil {
			log.Debug("public ip lookup failed", "endpoint", endpoint, "err", err)
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			continue
		}
		ip := strings.TrimSpace(string(body))
		if net.ParseIP(ip) != nil {
			return ip
		}
	}
	return ""
}
