package server

import (
	"bufio"
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/butialabs/proxywi/internal/storage"
	"github.com/butialabs/proxywi/internal/tunnel"
)

type HTTPProxy struct {
	Registry *Registry
	Gate     *AuthGate
	Store    *storage.Store
	Log      *slog.Logger
	Hub      *Hub
}

// IsProxyRequest reports whether r should be handled as a forward-proxy
// request (CONNECT tunnel or absolute-form URI forwarding) rather than
// as a request for a resource on this server.
func IsProxyRequest(r *http.Request) bool {
	if r.Method == http.MethodConnect {
		return true
	}
	if r.URL != nil && r.URL.IsAbs() && r.URL.Host != "" {
		return true
	}
	return false
}

func (p *HTTPProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sourceIP, _, _ := net.SplitHostPort(r.RemoteAddr)

	if err := p.Gate.CheckPreAuth(ctx, sourceIP); err != nil {
		http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		p.logEvent(ctx, 0, "", 0, "", r.Host, sourceIP, "http", "denied", 0, 0, 0)
		return
	}

	user, pass, ok := parseProxyAuth(r.Header.Get("Proxy-Authorization"))
	if !ok {
		w.Header().Set("Proxy-Authenticate", `Basic realm="proxywi"`)
		http.Error(w, "Proxy Authentication Required", http.StatusProxyAuthRequired)
		return
	}

	u, authed := p.Gate.VerifyCredentials(ctx, user, pass)
	if !authed {
		p.Gate.RegisterFailure(ctx, sourceIP, user, "http")
		w.Header().Set("Proxy-Authenticate", `Basic realm="proxywi"`)
		http.Error(w, "Proxy Authentication Required", http.StatusProxyAuthRequired)
		p.logEvent(ctx, 0, user, 0, "", r.Host, sourceIP, "http", "denied", 0, 0, 0)
		return
	}
	if !p.Gate.VerifySourceAllowed(u, sourceIP) {
		p.Gate.RegisterFailure(ctx, sourceIP, user, "http")
		http.Error(w, "Forbidden", http.StatusForbidden)
		p.logEvent(ctx, u.ID, u.Username, 0, "", r.Host, sourceIP, "http", "denied", 0, 0, 0)
		return
	}

	agent, err := p.Registry.PickNext()
	if err != nil {
		http.Error(w, "No upstream agent available", http.StatusBadGateway)
		p.logEvent(ctx, u.ID, u.Username, 0, "", r.Host, sourceIP, "http", "failed", 0, 0, 0)
		return
	}

	target := r.Host
	if r.Method != http.MethodConnect && r.URL.IsAbs() {
		target = r.URL.Host
	}
	if !strings.Contains(target, ":") {
		if r.Method == http.MethodConnect {
			target += ":443"
		} else {
			target += ":80"
		}
	}

	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	upstream, err := agent.OpenProxyStream(dialCtx, target)
	cancel()
	if err != nil {
		http.Error(w, "Upstream dial failed", http.StatusBadGateway)
		p.logEvent(ctx, u.ID, u.Username, agent.ID, agent.Name, target, sourceIP, "http", "failed", 0, 0, 0)
		return
	}
	defer upstream.Close()

	upReader := bufio.NewReader(upstream)
	var reply tunnel.ProxyReply
	if err := tunnel.ReadJSONLine(upReader, &reply); err != nil {
		http.Error(w, "Upstream reply error", http.StatusBadGateway)
		p.logEvent(ctx, u.ID, u.Username, agent.ID, agent.Name, target, sourceIP, "http", "failed", 0, 0, 0)
		return
	}
	if !reply.OK {
		http.Error(w, "Upstream: "+reply.Error, http.StatusBadGateway)
		p.logEvent(ctx, u.ID, u.Username, agent.ID, agent.Name, target, sourceIP, "http", "failed", 0, 0, 0)
		return
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack not supported", http.StatusInternalServerError)
		p.logEvent(ctx, u.ID, u.Username, agent.ID, agent.Name, target, sourceIP, "http", "failed", 0, 0, 0)
		return
	}
	clientConn, bufrw, err := hj.Hijack()
	if err != nil {
		p.logEvent(ctx, u.ID, u.Username, agent.ID, agent.Name, target, sourceIP, "http", "failed", 0, 0, 0)
		return
	}
	defer clientConn.Close()

	start := time.Now()
	var bytesIn, bytesOut int64

	if r.Method == http.MethodConnect {
		if _, err := bufrw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		if err := bufrw.Flush(); err != nil {
			return
		}
	} else {
		if err := r.Write(upstream); err != nil {
			return
		}
	}

	if buffered := bufrw.Reader.Buffered(); buffered > 0 && r.Method == http.MethodConnect {
		buf := make([]byte, buffered)
		if _, err := io.ReadFull(bufrw.Reader, buf); err == nil {
			_, _ = upstream.Write(buf)
			bytesOut += int64(buffered)
		}
	}

	bytesIn, bytesOut = pipe(clientConn, upstream, bytesIn, bytesOut)
	p.logEvent(ctx, u.ID, u.Username, agent.ID, agent.Name, target, sourceIP, "http", "ok",
		bytesIn, bytesOut, time.Since(start).Milliseconds())
}

func (p *HTTPProxy) logEvent(ctx context.Context,
	userID int64, user string,
	clientID int64, clientName string,
	target, sourceIP, proto, outcome string,
	bIn, bOut, durMS int64) {
	ev := storage.ProxyEvent{
		TS:         time.Now(),
		UserID:     userID,
		Username:   user,
		ClientID:   clientID,
		ClientName: clientName,
		TargetHost: target,
		SourceIP:   sourceIP,
		Protocol:   proto,
		Outcome:    outcome,
		BytesIn:    bIn,
		BytesOut:   bOut,
		DurationMS: durMS,
	}
	id, err := p.Store.InsertProxyEvent(ctx, ev)
	if err != nil {
		p.Log.Warn("proxy event insert", "err", err)
		return
	}
	if p.Hub != nil {
		p.Hub.Publish(Event{
			Type: "proxy_event",
			Data: ProxyLogEvent{
				ID: id, TS: ev.TS.Unix(),
				SourceIP: sourceIP, User: user,
				ClientID: clientID, ClientName: clientName,
				Target: target, Protocol: proto,
				BytesIn: bIn, BytesOut: bOut,
				DurationMS: durMS, Outcome: outcome,
			},
		})
	}
}

func pipe(client net.Conn, upstream net.Conn, inSeed, outSeed int64) (int64, int64) {
	inCh := make(chan int64, 1)
	outCh := make(chan int64, 1)
	go func() {
		n, _ := io.Copy(upstream, client)
		if cw, ok := upstream.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
		outCh <- n
	}()
	go func() {
		n, _ := io.Copy(client, upstream)
		if cw, ok := client.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
		inCh <- n
	}()
	out := <-outCh
	in := <-inCh
	return inSeed + in, outSeed + out
}

func parseProxyAuth(h string) (user, pass string, ok bool) {
	const prefix = "Basic "
	if !strings.HasPrefix(h, prefix) {
		return
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(h, prefix))
	if err != nil {
		return
	}
	s := string(raw)
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return
	}
	return s[:i], s[i+1:], true
}

