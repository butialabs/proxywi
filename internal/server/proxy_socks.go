package server

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"strconv"
	"time"

	"github.com/butialabs/proxywi/internal/storage"
	"github.com/butialabs/proxywi/internal/tunnel"
)

type SOCKSProxy struct {
	Registry *Registry
	Gate     *AuthGate
	Store    *storage.Store
	Log      *slog.Logger
	Hub      *Hub
}

const (
	socksVer5          byte = 0x05
	socksAuthUserPass  byte = 0x02
	socksAuthNone      byte = 0x00
	socksAuthNoAccept  byte = 0xFF
	socksCmdConnect    byte = 0x01
	socksAtypIPv4      byte = 0x01
	socksAtypDomain    byte = 0x03
	socksAtypIPv6      byte = 0x04
	socksRepSuccess    byte = 0x00
	socksRepGeneralErr byte = 0x01
	socksRepNetUnreach byte = 0x03
	socksRepHostUnreac byte = 0x04
	socksRepConnRefuse byte = 0x05
	socksRepCmdNotSup  byte = 0x07
	socksRepAtypNotSup byte = 0x08
)

var socksReplyZero = []byte{0, 0, 0, 0, 0, 0}

func (s *SOCKSProxy) Serve(ln net.Listener) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.handle(conn)
	}
}

func (s *SOCKSProxy) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	ctx := context.Background()

	sourceIP, _, _ := net.SplitHostPort(conn.RemoteAddr().String())

	if err := s.Gate.CheckPreAuth(ctx, sourceIP); err != nil {
		s.logEvent(ctx, 0, "", 0, "", "", sourceIP, "socks", "denied", 0, 0, 0)
		return
	}

	br := bufio.NewReader(conn)

	methods, ok := readGreeting(br)
	if !ok {
		return
	}
	if !containsByte(methods, socksAuthUserPass) {
		_, _ = conn.Write([]byte{socksVer5, socksAuthNoAccept})
		return
	}
	if _, err := conn.Write([]byte{socksVer5, socksAuthUserPass}); err != nil {
		return
	}

	username, password, ok := readUserPass(br)
	if !ok {
		return
	}

	u, authed := s.Gate.VerifyCredentials(ctx, username, password)
	if !authed {
		s.Gate.RegisterFailure(ctx, sourceIP, username, "socks")
		_, _ = conn.Write([]byte{0x01, 0x01})
		s.logEvent(ctx, 0, username, 0, "", "", sourceIP, "socks", "denied", 0, 0, 0)
		return
	}
	if !s.Gate.VerifySourceAllowed(u, sourceIP) {
		s.Gate.RegisterFailure(ctx, sourceIP, username, "socks")
		_, _ = conn.Write([]byte{0x01, 0x01})
		s.logEvent(ctx, u.ID, u.Username, 0, "", "", sourceIP, "socks", "denied", 0, 0, 0)
		return
	}
	if _, err := conn.Write([]byte{0x01, 0x00}); err != nil {
		return
	}

	target, ok := readRequest(br, conn)
	if !ok {
		return
	}

	agent, err := s.Registry.PickNextAllowed(u.AllowedClientIDs)
	if err != nil {
		_, _ = conn.Write(socksReply(socksRepNetUnreach))
		s.logEvent(ctx, u.ID, u.Username, 0, "", target, sourceIP, "socks", "failed", 0, 0, 0)
		return
	}

	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	upstream, err := agent.OpenProxyStream(dialCtx, target)
	cancel()
	if err != nil {
		_, _ = conn.Write(socksReply(socksRepHostUnreac))
		s.logEvent(ctx, u.ID, u.Username, agent.ID, agent.Name, target, sourceIP, "socks", "failed", 0, 0, 0)
		return
	}
	defer upstream.Close()

	upReader := bufio.NewReader(upstream)
	var reply tunnel.ProxyReply
	if err := tunnel.ReadJSONLine(upReader, &reply); err != nil {
		_, _ = conn.Write(socksReply(socksRepGeneralErr))
		s.logEvent(ctx, u.ID, u.Username, agent.ID, agent.Name, target, sourceIP, "socks", "failed", 0, 0, 0)
		return
	}
	if !reply.OK {
		_, _ = conn.Write(socksReply(socksRepConnRefuse))
		s.logEvent(ctx, u.ID, u.Username, agent.ID, agent.Name, target, sourceIP, "socks", "failed", 0, 0, 0)
		return
	}

	if _, err := conn.Write(socksReply(socksRepSuccess)); err != nil {
		return
	}
	_ = conn.SetDeadline(time.Time{})

	start := time.Now()
	var bytesIn, bytesOut int64

	if buffered := br.Buffered(); buffered > 0 {
		buf := make([]byte, buffered)
		if _, err := io.ReadFull(br, buf); err == nil {
			_, _ = upstream.Write(buf)
			bytesOut += int64(buffered)
		}
	}

	bytesIn, bytesOut = pipe(conn, upstream, bytesIn, bytesOut)
	s.logEvent(ctx, u.ID, u.Username, agent.ID, agent.Name, target, sourceIP, "socks", "ok",
		bytesIn, bytesOut, time.Since(start).Milliseconds())
}

func (s *SOCKSProxy) logEvent(ctx context.Context,
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
	id, err := s.Store.InsertProxyEvent(ctx, ev)
	if err != nil {
		s.Log.Warn("proxy event insert", "err", err)
		return
	}
	if s.Hub != nil {
		s.Hub.Publish(Event{
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

func readGreeting(br *bufio.Reader) ([]byte, bool) {
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(br, hdr); err != nil {
		return nil, false
	}
	if hdr[0] != socksVer5 {
		return nil, false
	}
	methods := make([]byte, int(hdr[1]))
	if _, err := io.ReadFull(br, methods); err != nil {
		return nil, false
	}
	return methods, true
}

func readUserPass(br *bufio.Reader) (string, string, bool) {
	ver := make([]byte, 1)
	if _, err := io.ReadFull(br, ver); err != nil {
		return "", "", false
	}
	if ver[0] != 0x01 {
		return "", "", false
	}
	lb := make([]byte, 1)
	if _, err := io.ReadFull(br, lb); err != nil {
		return "", "", false
	}
	uname := make([]byte, int(lb[0]))
	if _, err := io.ReadFull(br, uname); err != nil {
		return "", "", false
	}
	if _, err := io.ReadFull(br, lb); err != nil {
		return "", "", false
	}
	pw := make([]byte, int(lb[0]))
	if _, err := io.ReadFull(br, pw); err != nil {
		return "", "", false
	}
	return string(uname), string(pw), true
}

func readRequest(br *bufio.Reader, conn net.Conn) (string, bool) {
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(br, hdr); err != nil {
		return "", false
	}
	if hdr[0] != socksVer5 {
		return "", false
	}
	if hdr[1] != socksCmdConnect {
		_, _ = conn.Write(socksReply(socksRepCmdNotSup))
		return "", false
	}

	var host string
	switch hdr[3] {
	case socksAtypIPv4:
		ip := make([]byte, 4)
		if _, err := io.ReadFull(br, ip); err != nil {
			return "", false
		}
		host = net.IP(ip).String()
	case socksAtypDomain:
		lb := make([]byte, 1)
		if _, err := io.ReadFull(br, lb); err != nil {
			return "", false
		}
		d := make([]byte, int(lb[0]))
		if _, err := io.ReadFull(br, d); err != nil {
			return "", false
		}
		host = string(d)
	case socksAtypIPv6:
		ip := make([]byte, 16)
		if _, err := io.ReadFull(br, ip); err != nil {
			return "", false
		}
		host = net.IP(ip).String()
	default:
		_, _ = conn.Write(socksReply(socksRepAtypNotSup))
		return "", false
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(br, portBytes); err != nil {
		return "", false
	}
	port := binary.BigEndian.Uint16(portBytes)
	return net.JoinHostPort(host, strconv.Itoa(int(port))), true
}

func socksReply(status byte) []byte {
	out := make([]byte, 0, 10)
	out = append(out, socksVer5, status, 0x00, socksAtypIPv4)
	out = append(out, socksReplyZero...)
	return out
}

func containsByte(b []byte, v byte) bool {
	for _, c := range b {
		if c == v {
			return true
		}
	}
	return false
}
