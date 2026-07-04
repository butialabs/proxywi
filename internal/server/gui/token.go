package gui

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/butialabs/proxywi/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

func (g *GUI) clientByToken(ctx context.Context, token string) (*storage.Client, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, nil
	}
	sha256h := storage.SHA256Token(token)
	tokenID := storage.TokenIDFromToken(token)
	if tokenID != "" {
		cl, err := g.Store.ClientByTokenID(ctx, tokenID)
		if err != nil {
			return nil, err
		}
		if cl != nil {
			if cl.TokenHashSHA256 != "" {
				if cl.TokenHashSHA256 == sha256h {
					return cl, nil
				}
			} else if bcrypt.CompareHashAndPassword([]byte(cl.TokenHash), []byte(token)) == nil {
				go func() { _ = g.Store.SetClientTokenSHA256(context.Background(), cl.ID, sha256h) }()
				return cl, nil
			}
		}
	}
	// Fallback for legacy tokens without token_id.
	hashes, err := g.Store.AllClientTokenHashes(ctx)
	if err != nil {
		return nil, err
	}
	for id, h := range hashes {
		if h.SHA256 != "" && h.SHA256 == sha256h {
			return g.Store.ClientByID(ctx, id)
		}
	}
	for id, h := range hashes {
		if h.SHA256 == "" && bcrypt.CompareHashAndPassword([]byte(h.Bcrypt), []byte(token)) == nil {
			cl, err := g.Store.ClientByID(ctx, id)
			if err != nil {
				return nil, err
			}
			go func(clientID int64) { _ = g.Store.SetClientTokenSHA256(context.Background(), clientID, sha256h) }(id)
			return cl, nil
		}
	}
	return nil, nil
}

func (g *GUI) getTokenLogin(w http.ResponseWriter, r *http.Request) {
	// Token login now lives on the unified /login page.
	http.Redirect(w, r, "/login", http.StatusFound)
}

func (g *GUI) postTokenLogin(w http.ResponseWriter, r *http.Request) {
	v := g.view(r)
	_ = r.ParseForm()
	token := r.Form.Get("token")
	client, err := g.clientByToken(r.Context(), token)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	if client == nil {
		time.Sleep(500 * time.Millisecond)
		g.renderFlash(w, r, "login.html", v.tr("token_login.bad_token"), "danger", "Sign in")
		return
	}
	sid, err := g.Store.CreateTokenSession(r.Context(), client.ID, 24*time.Hour)
	if err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}
	g.setTokenSession(w, sid)
	http.Redirect(w, r, "/t/logs", http.StatusFound)
}

func (g *GUI) postTokenLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(tokenSessionCookie); err == nil {
		_ = g.Store.DeleteTokenSession(r.Context(), c.Value)
	}
	g.clearTokenSession(w)
	http.Redirect(w, r, "/token-login", http.StatusFound)
}

type tokenLogView struct {
	TSHuman    string
	Target     string
	Protocol   string
	Outcome    string
	BytesInH   string
	BytesOutH  string
	ConnectMS  int64
	DurationMS int64
}

func (g *GUI) getTokenLogs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	clientID, _ := ctx.Value(clientIDKey).(int64)
	if clientID == 0 {
		http.Redirect(w, r, "/token-login", http.StatusFound)
		return
	}
	client, _ := g.Store.ClientByID(ctx, clientID)
	clientName := ""
	if client != nil {
		clientName = client.Name
	}

	since := time.Now().Add(-24 * time.Hour)
	events, _ := g.Store.ListProxyEventsFiltered(ctx, storage.ProxyEventFilter{ClientID: clientID, Since: since}, 200, 0)
	views := make([]tokenLogView, 0, len(events))
	for _, e := range events {
		views = append(views, tokenLogView{
			TSHuman:    e.TS.Format("Jan 02 15:04:05"),
			Target:     e.TargetHost,
			Protocol:   e.Protocol,
			Outcome:    e.Outcome,
			BytesInH:   humanBytes(e.BytesIn),
			BytesOutH:  humanBytes(e.BytesOut),
			ConnectMS:  e.ConnectMS,
			DurationMS: e.DurationMS,
		})
	}
	g.render(w, r, "token_logs.html", map[string]any{
		"Title":      "Logs",
		"TokenUser":  clientName,
		"ClientName": clientName,
		"Active":     "token_logs",
		"Events":     views,
	})
}

type domainStatView struct {
	Domain        string
	Requests      int64
	BytesInH      string
	BytesOutH     string
	AvgDurationMS int64
	AvgConnectMS  int64
}

func (g *GUI) getTokenStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	clientID, _ := ctx.Value(clientIDKey).(int64)
	if clientID == 0 {
		http.Redirect(w, r, "/token-login", http.StatusFound)
		return
	}
	client, _ := g.Store.ClientByID(ctx, clientID)
	clientName := ""
	if client != nil {
		clientName = client.Name
	}

	period := r.URL.Query().Get("period")
	var since time.Time
	switch period {
	case "month":
		since = time.Now().Add(-30 * 24 * time.Hour)
	default:
		period = "week"
		since = time.Now().Add(-7 * 24 * time.Hour)
	}

	domainStats, _ := g.Store.DomainStats(ctx, clientID, since)
	views := make([]domainStatView, 0, len(domainStats))
	for _, s := range domainStats {
		views = append(views, domainStatView{
			Domain:        s.Domain,
			Requests:      s.Requests,
			BytesInH:      humanBytes(s.BytesIn),
			BytesOutH:     humanBytes(s.BytesOut),
			AvgDurationMS: s.AvgDurationMS,
			AvgConnectMS:  s.AvgConnectMS,
		})
	}
	g.render(w, r, "token_stats.html", map[string]any{
		"Title":      "Stats",
		"TokenUser":  clientName,
		"ClientName": clientName,
		"Active":     "token_stats",
		"Stats":      views,
		"Period":     period,
	})
}
