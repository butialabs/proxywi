package dns

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"time"
)

func NewDoHResolver(endpoints []string) *net.Resolver {
	hc := &http.Client{Timeout: 10 * time.Second}
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return &dohConn{ctx: ctx, endpoints: endpoints, hc: hc}, nil
		},
	}
}

type dohConn struct {
	ctx       context.Context
	endpoints []string
	hc        *http.Client
	wbuf      []byte
	rbuf      []byte
	roff      int
}

func (c *dohConn) Write(b []byte) (int, error) {
	c.wbuf = append(c.wbuf, b...)
	if len(c.wbuf) < 2 {
		return len(b), nil
	}
	msgLen := int(binary.BigEndian.Uint16(c.wbuf[:2]))
	if len(c.wbuf) < 2+msgLen {
		return len(b), nil
	}
	query := c.wbuf[2 : 2+msgLen]
	c.wbuf = c.wbuf[2+msgLen:]

	resp, err := c.post(query)
	if err != nil {
		return 0, err
	}
	if len(resp) > math.MaxUint16 {
		return 0, fmt.Errorf("DoH response too large: %d bytes", len(resp))
	}
	c.rbuf = make([]byte, 2, 2+len(resp))
	binary.BigEndian.PutUint16(c.rbuf, uint16(len(resp)))
	c.rbuf = append(c.rbuf, resp...)
	c.roff = 0
	return len(b), nil
}

func (c *dohConn) post(query []byte) ([]byte, error) {
	var lastErr error
	for _, ep := range c.endpoints {
		req, err := http.NewRequestWithContext(c.ctx, http.MethodPost, ep, bytes.NewReader(query))
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("Content-Type", "application/dns-message")
		req.Header.Set("Accept", "application/dns-message")

		resp, err := c.hc.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 65535))
		_ = resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("DoH %s: HTTP %d", ep, resp.StatusCode)
			continue
		}
		return body, nil
	}
	if lastErr != nil {
		return nil, fmt.Errorf("all DoH endpoints failed: %w", lastErr)
	}
	return nil, errors.New("no DoH endpoints configured")
}

func (c *dohConn) Read(b []byte) (int, error) {
	if c.roff >= len(c.rbuf) {
		return 0, io.EOF
	}
	n := copy(b, c.rbuf[c.roff:])
	c.roff += n
	return n, nil
}

func (c *dohConn) Close() error                       { return nil }
func (c *dohConn) LocalAddr() net.Addr                { return dohAddr{} }
func (c *dohConn) RemoteAddr() net.Addr               { return dohAddr{} }
func (c *dohConn) SetDeadline(_ time.Time) error      { return nil }
func (c *dohConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *dohConn) SetWriteDeadline(_ time.Time) error { return nil }

type dohAddr struct{}

func (dohAddr) Network() string { return "doh" }
func (dohAddr) String() string  { return "doh" }
