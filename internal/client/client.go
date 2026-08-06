package client

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	internaldns "github.com/butialabs/proxywi/internal/dns"
	"github.com/butialabs/proxywi/internal/tunnel"
	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

type Agent struct {
	ServerURL      string
	Token          string
	TLSInsecure    bool
	AllowedTargets []string
	DeniedTargets  []string
	DoHProviders   []string
	AgentVersion   string
	Log            *slog.Logger

	aclOnce  sync.Once
	acl      targetACL
	resolver *net.Resolver
	metaMu   sync.Mutex
}

func (a *Agent) Run(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	dialOpts := &websocket.DialOptions{}
	if a.TLSInsecure {
		dialOpts.HTTPClient = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}
	}
	ws, resp, err := websocket.Dial(dialCtx, a.ServerURL+"/ws/control", dialOpts)
	if err != nil {
		return fmt.Errorf("ws dial: %w", err)
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	ws.SetReadLimit(-1)

	defer func() { _ = ws.CloseNow() }()

	if err := ws.Write(dialCtx, websocket.MessageText, mustJSON(tunnel.Handshake{
		Version:      tunnel.ProtocolVersion,
		Token:        a.Token,
		AgentVersion: a.AgentVersion,
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
	defer func() { _ = netConn.Close() }()

	yCfg := yamux.DefaultConfig()
	yCfg.EnableKeepAlive = true
	yCfg.KeepAliveInterval = 20 * time.Second
	yCfg.LogOutput = io.Discard

	session, err := yamux.Client(netConn, yCfg)
	if err != nil {
		return fmt.Errorf("yamux client: %w", err)
	}
	defer func() { _ = session.Close() }()

	metaStream, err := session.Open()
	if err != nil {
		return fmt.Errorf("open meta stream: %w", err)
	}
	defer func() { _ = metaStream.Close() }()

	var counters counters
	go a.metaWriteLoop(ctx, metaStream, &counters)
	go a.metaReadLoop(ctx, metaStream)

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
	defer func() { _ = stream.Close() }()
	_ = stream.SetDeadline(time.Now().Add(10 * time.Second))

	br := bufio.NewReader(stream)
	var req tunnel.ProxyRequest
	if err := tunnel.ReadJSONLine(br, &req); err != nil {
		a.Log.Warn("read proxy request", "err", err)
		return
	}

	dialAddr, allowed := a.resolveAllowedTarget(ctx, req.Target)
	if !allowed {
		a.Log.Warn("target blocked by ACL", "target", req.Target)
		_ = tunnel.WriteJSONLine(stream, tunnel.ProxyReply{OK: false, Error: "target not allowed"})
		return
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second, Resolver: a.resolver}
	target, err := dialer.DialContext(ctx, "tcp", dialAddr)
	if err != nil {
		_ = tunnel.WriteJSONLine(stream, tunnel.ProxyReply{OK: false, Error: err.Error()})
		return
	}
	defer func() { _ = target.Close() }()

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
		halfCloseConn(target)
		done <- struct{}{}
	}()
	go func() {
		n, _ := copyCounted(stream, target)
		c.bytesIn.Add(n)
		halfCloseConn(stream)
		done <- struct{}{}
	}()
	<-done
	<-done
}

func (a *Agent) sendMeta(stream net.Conn, msg tunnel.MetaMessage) error {
	a.metaMu.Lock()
	defer a.metaMu.Unlock()
	return tunnel.WriteJSONLine(stream, msg)
}

func (a *Agent) metaWriteLoop(ctx context.Context, stream net.Conn, c *counters) {
	_ = a.sendMeta(stream, tunnel.MetaMessage{Type: "heartbeat"})
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
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
			if err := a.sendMeta(stream, msg); err != nil {
				return
			}
		}
	}
}

func (a *Agent) metaReadLoop(ctx context.Context, stream net.Conn) {
	br := bufio.NewReader(stream)
	for {
		var msg tunnel.MetaMessage
		if err := tunnel.ReadJSONLine(br, &msg); err != nil {
			return
		}
		if msg.Type == "ping" {
			_ = a.sendMeta(stream, tunnel.MetaMessage{Type: "pong", SentAt: msg.SentAt})
		}
	}
}

func copyCounted(dst io.Writer, src io.Reader) (int64, error) {
	return io.Copy(dst, src)
}

func halfCloseConn(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	} else {
		_ = c.Close()
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

type targetACL struct {
	deniedNets   []netip.Prefix
	allowedNets  []netip.Prefix
	allowedHosts map[string]bool
	deniedHosts  map[string]bool
}

var defaultDeniedPrefixes = []string{
	"127.0.0.0/8",
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"169.254.0.0/16",
	"224.0.0.0/4",
	"255.255.255.255/32",
	"::1/128",
	"fe80::/10",
	"ff00::/8",
}

func (a *Agent) initACL() {
	a.aclOnce.Do(func() {
		a.acl.deniedNets = parsePrefixes(append([]string{}, a.DeniedTargets...))
		if len(a.acl.deniedNets) == 0 {
			a.acl.deniedNets = parsePrefixes(defaultDeniedPrefixes)
		}
		a.acl.allowedNets = parsePrefixes(a.AllowedTargets)
		a.acl.allowedHosts, a.acl.deniedHosts = parseHosts(a.AllowedTargets), parseHosts(a.DeniedTargets)
		if len(a.DoHProviders) > 0 {
			a.resolver = internaldns.NewDoHResolver(a.DoHProviders)
		} else {
			a.resolver = net.DefaultResolver
		}
	})
}

func parsePrefixes(items []string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(items))
	for _, it := range items {
		it = strings.TrimSpace(it)
		if it == "" {
			continue
		}
		if p, err := netip.ParsePrefix(it); err == nil {
			out = append(out, p)
			continue
		}
		if addr, err := netip.ParseAddr(it); err == nil {
			if p, err := addr.Prefix(addr.BitLen()); err == nil {
				out = append(out, p)
			}
		}
	}
	return out
}

func parseHosts(items []string) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, it := range items {
		it = strings.TrimSpace(it)
		if it == "" {
			continue
		}
		if _, err := netip.ParsePrefix(it); err == nil {
			continue
		}
		if _, err := netip.ParseAddr(it); err == nil {
			continue
		}
		out[strings.ToLower(it)] = true
	}
	return out
}

func (a *Agent) resolveAllowedTarget(ctx context.Context, target string) (string, bool) {
	a.initACL()
	if target == "" {
		return "", false
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		host, port = target, ""
	}
	host = strings.ToLower(strings.TrimSpace(host))

	explicitlyAllowed := a.acl.allowedHosts[host]

	addrs, err := a.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		if a.acl.deniedHosts[host] {
			return "", false
		}
		return target, true
	}

	var chosen netip.Addr
	for _, ia := range addrs {
		addr, ok := netip.AddrFromSlice(ia.IP)
		if !ok {
			continue
		}
		addr = addr.Unmap()
		if !explicitlyAllowed && a.isDeniedAddr(addr) {
			return "", false
		}
		if !chosen.IsValid() {
			chosen = addr
		}
	}
	if !chosen.IsValid() {
		return "", false
	}
	if port == "" {
		return chosen.String(), true
	}
	return net.JoinHostPort(chosen.String(), port), true
}

func (a *Agent) isDeniedAddr(addr netip.Addr) bool {
	for _, p := range a.acl.allowedNets {
		if p.Contains(addr) {
			return false
		}
	}
	for _, p := range a.acl.deniedNets {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
