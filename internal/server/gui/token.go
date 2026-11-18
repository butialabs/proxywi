package gui

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/butialabs/proxywi/internal/storage"
)

func (g *GUI) clientByToken(ctx context.Context, token string) (*storage.Client, error) {
	return g.Store.ClientByToken(ctx, token)
}

func (g *GUI) getTokenLogin(w http.ResponseWriter, r *http.Request) {
	// Token login now lives on the unified /login page.
	http.Redirect(w, r, "/login", http.StatusFound)
}

func (g *GUI) postTokenLogin(w http.ResponseWriter, r *http.Request) {
	v := g.view(r)
	if g.badForm(w, r) {
		return
	}
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
		if err := g.Store.DeleteTokenSession(r.Context(), c.Value); err != nil {
			g.Log.Warn("delete token session", "err", err)
		}
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
	client, err := g.Store.ClientByID(ctx, clientID)
	if g.dbError(w, "client by id", err) {
		return
	}
	clientName := ""
	if client != nil {
		clientName = client.Name
	}

	since := time.Now().Add(-24 * time.Hour)
	events, err := g.Store.ListProxyEventsFiltered(ctx, storage.ProxyEventFilter{ClientID: clientID, Since: since}, 200, 0)
	if g.dbError(w, "list proxy events", err) {
		return
	}
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
	client, err := g.Store.ClientByID(ctx, clientID)
	if g.dbError(w, "client by id", err) {
		return
	}
	clientName := ""
	if client != nil {
		clientName = client.Name
	}

	data, err := g.domainStatsPage(ctx, r.URL.Query(), clientID)
	if g.dbError(w, "domain stats", err) {
		return
	}
	data["Title"] = "Stats"
	data["TokenUser"] = clientName
	data["ClientName"] = clientName
	data["Active"] = "token_stats"
	g.render(w, r, "token_stats.html", data)
}

const statsPageSize = 20

func (g *GUI) domainStatsPage(ctx context.Context, q url.Values, clientID int64) (map[string]any, error) {
	period := q.Get("period")
	var since time.Time
	switch period {
	case "month":
		since = time.Now().Add(-30 * 24 * time.Hour)
	default:
		period = "week"
		since = time.Now().Add(-7 * 24 * time.Hour)
	}
	lq := parseListQuery(q, storage.DomainStatSortKeys, "requests", "desc", "period").
		withDescFirst("requests", "in", "out", "connect", "duration")

	sq := storage.DomainStatsQuery{ClientID: clientID, Since: since, Sort: lq.Sort, Desc: lq.Desc()}
	total, err := g.Store.CountDomainStats(ctx, sq)
	if err != nil {
		return nil, err
	}
	offset, data := lq.paginate(total, statsPageSize)
	sq.Limit, sq.Offset = statsPageSize, offset
	domainStats, err := g.Store.DomainStats(ctx, sq)
	if err != nil {
		return nil, err
	}
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
	data["Stats"] = views
	data["Period"] = period
	data["Q"] = lq
	return data, nil
}
