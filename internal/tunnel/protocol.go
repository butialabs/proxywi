package tunnel

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const ProtocolVersion = 1

type Handshake struct {
	Version int    `json:"version"`
	Token   string `json:"token"`
	Name    string `json:"name"`
	SelfReportedIP string `json:"self_reported_ip,omitempty"`
}

type HandshakeAck struct {
	OK       bool   `json:"ok"`
	ClientID int64  `json:"client_id,omitempty"`
	Error    string `json:"error,omitempty"`
}

func WriteJSONLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

func ReadJSONLine(r *bufio.Reader, v any) error {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return err
	}
	return json.Unmarshal(line, v)
}

type ProxyRequest struct {
	Target string `json:"target"` // host:port
}

type ProxyReply struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type MetaMessage struct {
	Type string `json:"type"` // heartbeat | metrics

	BytesIn     int64 `json:"bytes_in,omitempty"`
	BytesOut    int64 `json:"bytes_out,omitempty"`
	ActiveConns int   `json:"active_conns,omitempty"`
}

var ErrBadHandshake = errors.New("bad handshake")

func ValidateHandshake(h *Handshake) error {
	if h.Version != ProtocolVersion {
		return fmt.Errorf("%w: unsupported protocol version %d (want %d)", ErrBadHandshake, h.Version, ProtocolVersion)
	}
	if h.Token == "" {
		return fmt.Errorf("%w: empty token", ErrBadHandshake)
	}
	return nil
}
