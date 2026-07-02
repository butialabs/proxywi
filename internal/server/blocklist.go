package server

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

type Blocklist struct {
	mu      sync.RWMutex
	domains map[string]bool
}

func NewBlocklist() *Blocklist {
	return &Blocklist{domains: make(map[string]bool)}
}

func (b *Blocklist) IsBlocked(host string) bool {
	b.mu.RLock()
	blocked := b.domains[host]
	b.mu.RUnlock()
	return blocked
}

func (b *Blocklist) Replace(domains map[string]bool) {
	b.mu.Lock()
	b.domains = domains
	b.mu.Unlock()
}

func (b *Blocklist) Len() int {
	b.mu.RLock()
	n := len(b.domains)
	b.mu.RUnlock()
	return n
}

var DefaultBlocklistURLs = map[string]string{
	"adult":    "https://raw.githubusercontent.com/StevenBlack/hosts/master/alternates/porn/hosts",
	"gambling": "https://raw.githubusercontent.com/StevenBlack/hosts/master/alternates/gambling/hosts",
	"crypto":   "https://raw.githubusercontent.com/nicehash/nicehash-mining-pool-blocklist/main/mining-pool-blocklist.txt",
}

func FetchDomains(ctx context.Context, url string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("blocklist fetch %s: status %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024*1024))
	if err != nil {
		return nil, err
	}
	return parseHostsFile(body), nil
}

func parseHostsFile(data []byte) []string {
	skip := map[string]bool{
		"localhost": true, "0.0.0.0": true, "127.0.0.1": true, "::1": true,
	}
	var out []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		var domain string
		switch len(fields) {
		case 1:
			domain = fields[0]
		case 2:
			domain = fields[1]
		default:
			continue
		}
		domain = strings.ToLower(domain)
		if skip[domain] {
			continue
		}
		out = append(out, domain)
	}
	return out
}
