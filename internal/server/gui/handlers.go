package gui

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/butialabs/proxywi/internal/i18n"
	"github.com/butialabs/proxywi/internal/storage"
	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
)

type viewCtx struct {
	r    *http.Request
	lang string
	tr   i18n.Translator
}

func (g *GUI) view(r *http.Request) viewCtx {
	lang, tr := i18n.FromRequest(r)
	return viewCtx{r: r, lang: lang, tr: tr}
}

func (v viewCtx) common(extra map[string]any) map[string]any {
	if extra == nil {
		extra = map[string]any{}
	}
	extra["Lang"] = v.lang
	extra["Langs"] = i18n.Available()
	return extra
}

func (g *GUI) adminName(ctx context.Context) string {
	id, ok := ctx.Value(adminIDKey).(int64)
	if !ok {
		return ""
	}
	username, err := g.Store.AdminUsernameByID(ctx, id)
	if err != nil {
		g.Log.Error("lookup admin name", "admin_id", id, "err", err)
		return ""
	}
	return username
}

func (g *GUI) dbError(w http.ResponseWriter, op string, err error) bool {
	if err == nil {
		return false
	}
	g.Log.Error("store operation failed", "op", op, "err", err)
	http.Error(w, "database error", http.StatusInternalServerError)
	return true
}

func (g *GUI) badForm(w http.ResponseWriter, r *http.Request) bool {
	if err := r.ParseForm(); err != nil {
		g.Log.Warn("parse form", "path", r.URL.Path, "err", err)
		http.Error(w, "invalid form", http.StatusBadRequest)
		return true
	}
	return false
}

func (g *GUI) getLogin(w http.ResponseWriter, r *http.Request) {
	g.render(w, r, "login.html", map[string]any{"Title": "Sign in"})
}

func (g *GUI) postLogin(w http.ResponseWriter, r *http.Request) {
	v := g.view(r)
	if g.badForm(w, r) {
		return
	}
	username := strings.TrimSpace(r.Form.Get("username"))
	password := r.Form.Get("password")
	admin, err := g.Store.AdminByUsername(r.Context(), username)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	if admin == nil {
		time.Sleep(500 * time.Millisecond)
		g.renderFlash(w, r, "login.html", v.tr("login.bad_credentials"), "danger", "Sign in")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(password)); err != nil {
		time.Sleep(500 * time.Millisecond)
		g.renderFlash(w, r, "login.html", v.tr("login.bad_credentials"), "danger", "Sign in")
		return
	}
	sid, err := g.Store.CreateSession(r.Context(), admin.ID, 24*time.Hour)
	if err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}
	g.setSession(w, sid)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (g *GUI) postLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := g.Store.DeleteSession(r.Context(), c.Value); err != nil {
			g.Log.Warn("delete session", "err", err)
		}
	}
	g.clearSession(w)
	http.Redirect(w, r, "/login", http.StatusFound)
}

type clientOption struct {
	ID   int64
	Name string
}

type periodOption struct {
	Value string
	Label string
}

type period struct {
	duration time.Duration
	bucket   time.Duration
	labelKey string
}

var periodDefs = map[string]period{
	"1h":  {duration: time.Hour, bucket: time.Minute, labelKey: "dashboard.period_1h"},
	"6h":  {duration: 6 * time.Hour, bucket: 5 * time.Minute, labelKey: "dashboard.period_6h"},
	"24h": {duration: 24 * time.Hour, bucket: 15 * time.Minute, labelKey: "dashboard.period_24h"},
	"7d":  {duration: 7 * 24 * time.Hour, bucket: time.Hour, labelKey: "dashboard.period_7d"},
	"30d": {duration: 30 * 24 * time.Hour, bucket: 6 * time.Hour, labelKey: "dashboard.period_30d"},
}

var periodOrder = []string{"1h", "6h", "24h", "7d", "30d"}

func (g *GUI) getDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	v := g.view(r)

	q := r.URL.Query()
	periodKey := q.Get("period")
	if _, ok := periodDefs[periodKey]; !ok {
		periodKey = "1h"
	}
	p := periodDefs[periodKey]

	allClients, err := g.Store.ListClients(ctx)
	if g.dbError(w, "list clients", err) {
		return
	}

	since := time.Now().Add(-p.duration)
	mf := storage.MetricsFilter{Since: since, BucketSeconds: int64(p.bucket.Seconds())}
	samples, err := g.Store.Metrics(ctx, mf)
	if g.dbError(w, "metrics", err) {
		return
	}

	buckets := make(map[int64][2]int64, len(samples))
	for _, s := range samples {
		buckets[s.BucketTS] = [2]int64{s.BytesIn, s.BytesOut}
	}
	var labels []string
	var in, out []int64
	labelFmt := "15:04"
	if p.duration >= 48*time.Hour {
		labelFmt = "Jan 02 15:04"
	}
	for t := since.Truncate(p.bucket); !t.After(time.Now()); t = t.Add(p.bucket) {
		labels = append(labels, t.Format(labelFmt))
		pair := buckets[t.Unix()]
		in = append(in, pair[0])
		out = append(out, pair[1])
	}

	onlineAgents := g.Registry.Online()
	var totalIn, totalOut int64
	for _, s := range samples {
		totalIn += s.BytesIn
		totalOut += s.BytesOut
	}

	pos := make([]periodOption, 0, len(periodOrder))
	for _, k := range periodOrder {
		pos = append(pos, periodOption{Value: k, Label: v.tr(periodDefs[k].labelKey)})
	}

	g.render(w, r, "dashboard.html", map[string]any{
		"Title":         "Dashboard",
		"Active":        "dashboard",
		"User":          g.adminName(ctx),
		"OnlineCount":   len(onlineAgents),
		"TotalClients":  len(allClients),
		"BytesInHuman":  humanBytes(totalIn),
		"BytesOutHuman": humanBytes(totalOut),
		"PeriodOptions": pos,
		"FilterPeriod":  periodKey,
		"PeriodHuman":   v.tr(p.labelKey),
		"DashboardData": map[string]any{
			"labels":  labels,
			"dataIn":  in,
			"dataOut": out,
			"tr": map[string]string{
				"in":  v.tr("dashboard.chart_in"),
				"out": v.tr("dashboard.chart_out"),
			},
		},
	})
}

const pingDegradedThresholdMS = 2000

type clientView struct {
	ID            int64
	Name          string
	AgentVersion  string
	Online        bool
	LastSeenHuman string
	PingMS        *int64
	PingStatus    string // "ok" | "degraded" | "unknown"

	lastSeen time.Time
}

var clientSortKeys = []string{"id", "name", "version", "status", "last_seen", "ping"}

func sortClientViews(views []clientView, lq listQuery) {
	desc := lq.Desc()
	slices.SortStableFunc(views, func(a, b clientView) int {
		var c int
		switch lq.Sort {
		case "name":
			c = cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		case "version":
			c = cmp.Compare(a.AgentVersion, b.AgentVersion)
		case "status":
			c = cmp.Compare(boolInt(a.Online), boolInt(b.Online))
		case "last_seen":
			c = a.lastSeen.Compare(b.lastSeen)
		case "ping":
			switch {
			case a.PingMS == nil && b.PingMS == nil:
			case a.PingMS == nil:
				return 1
			case b.PingMS == nil:
				return -1
			default:
				c = cmp.Compare(*a.PingMS, *b.PingMS)
			}
		}
		if c == 0 {
			c = cmp.Compare(a.ID, b.ID)
		}
		if desc {
			c = -c
		}
		return c
	})
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

const composeTokenPlaceholder = "<paste-your-token-here>"

func (g *GUI) getClients(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	v := g.view(r)
	list, err := g.Store.ListClients(ctx)
	if g.dbError(w, "list clients", err) {
		return
	}
	views := make([]clientView, 0, len(list))
	for _, c := range list {
		_, online := g.Registry.Get(c.ID)
		pingStatus := "unknown"
		if c.PingMS != nil {
			if *c.PingMS > pingDegradedThresholdMS {
				pingStatus = "degraded"
			} else {
				pingStatus = "ok"
			}
		}
		views = append(views, clientView{
			ID:            c.ID,
			Name:          c.Name,
			AgentVersion:  c.AgentVersion,
			Online:        online,
			LastSeenHuman: humanSince(c.LastSeen),
			PingMS:        c.PingMS,
			PingStatus:    pingStatus,
			lastSeen:      c.LastSeen,
		})
	}
	lq := parseListQuery(r.URL.Query(), clientSortKeys, "id", "desc").
		withDescFirst("id", "status", "last_seen")
	sortClientViews(views, lq)
	data := map[string]any{
		"Title":   "Clients",
		"Active":  "clients",
		"User":    g.adminName(ctx),
		"Clients": views,
		"Q":       lq,
		"ClientsData": map[string]string{
			"confirmTpl": v.tr("clients.confirm_regenerate"),
		},
	}

	tok := g.getFlashToken(w, r)
	if tok != "" {
		if id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64); err == nil {
			c, err := g.Store.ClientByID(ctx, id)
			if g.dbError(w, "client by id", err) {
				return
			}
			if c != nil {
				data["NewCompose"] = buildCompose(controlURL(r), tok)
				data["NewComposeName"] = c.Name
			}
		}
	}
	g.render(w, r, "clients.html", data)
}

func controlURL(r *http.Request) string {
	scheme := "ws"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "wss"
	}
	host := r.Host
	if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
		host = fh
	}
	return scheme + "://" + host
}

func buildCompose(controlURL, token string) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "services:\n")
	fmt.Fprintf(&b, "  proxywi-client:\n")
	fmt.Fprintf(&b, "    image: ghcr.io/butialabs/proxywi-client:latest\n")
	fmt.Fprintf(&b, "    restart: unless-stopped\n")
	fmt.Fprintf(&b, "    environment:\n")
	fmt.Fprintf(&b, "      PROXYWI_SERVER: %q\n", controlURL)
	fmt.Fprintf(&b, "      PROXYWI_TOKEN:  %q\n", token)
	return b.String()
}

func (g *GUI) postNewClient(w http.ResponseWriter, r *http.Request) {
	name, err := g.Store.GenerateUniqueClientName(r.Context())
	if err != nil {
		http.Error(w, "name error", http.StatusInternalServerError)
		return
	}
	token := randHex(32)
	hash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "hash error", http.StatusInternalServerError)
		return
	}
	id, err := g.Store.CreateClient(r.Context(), name, string(hash), storage.SHA256Token(token), storage.TokenIDFromToken(token))
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	g.setFlashToken(w, token)
	http.Redirect(w, r, fmt.Sprintf("/clients?id=%d", id), http.StatusFound)
}

func (g *GUI) postRegenerateClient(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	c, err := g.Store.ClientByID(r.Context(), id)
	if err != nil || c == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	token := randHex(32)
	hash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "hash error", http.StatusInternalServerError)
		return
	}
	if err := g.Store.UpdateClientToken(r.Context(), id, string(hash), storage.SHA256Token(token), storage.TokenIDFromToken(token)); err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	g.Registry.Disconnect(id)
	g.setFlashToken(w, token)
	http.Redirect(w, r, fmt.Sprintf("/clients?id=%d", id), http.StatusFound)
}

func (g *GUI) getClientCompose(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	c, err := g.Store.ClientByID(r.Context(), id)
	if err != nil || c == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(buildCompose(controlURL(r), composeTokenPlaceholder)))
}

func (g *GUI) postDeleteClient(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if g.dbError(w, "delete client", g.Store.DeleteClient(r.Context(), id)) {
		return
	}
	http.Redirect(w, r, "/clients", http.StatusFound)
}

type accessView struct {
	ID            int64
	Username      string
	LastUsedHuman string
	UsedCount     int

	lastUsed time.Time
}

var accessSortKeys = []string{"id", "user", "last_used", "used"}

func sortAccessViews(views []accessView, lq listQuery) {
	slices.SortStableFunc(views, func(a, b accessView) int {
		var c int
		switch lq.Sort {
		case "user":
			c = cmp.Compare(strings.ToLower(a.Username), strings.ToLower(b.Username))
		case "last_used":
			c = a.lastUsed.Compare(b.lastUsed)
		case "used":
			c = cmp.Compare(a.UsedCount, b.UsedCount)
		}
		if c == 0 {
			c = cmp.Compare(a.ID, b.ID)
		}
		if lq.Desc() {
			c = -c
		}
		return c
	})
}

func (g *GUI) getAccess(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	list, err := g.Store.ListUsers(ctx)
	if g.dbError(w, "list users", err) {
		return
	}
	usage, err := g.Store.UserUsageStats(ctx)
	if g.dbError(w, "user usage stats", err) {
		return
	}

	views := make([]accessView, 0, len(list))
	for _, u := range list {
		uu := usage[u.ID]
		lastUsed := "—"
		if uu.Count > 0 {
			lastUsed = humanSince(uu.LastUsed)
		}
		views = append(views, accessView{
			ID:            u.ID,
			Username:      u.Username,
			LastUsedHuman: lastUsed,
			UsedCount:     uu.Count,
			lastUsed:      uu.LastUsed,
		})
	}
	lq := parseListQuery(r.URL.Query(), accessSortKeys, "id", "asc").withDescFirst("last_used", "used")
	sortAccessViews(views, lq)

	proxyHost := g.Cfg.MainDomain

	g.render(w, r, "access.html", map[string]any{
		"Title":     "Proxy Access",
		"Active":    "access",
		"User":      g.adminName(ctx),
		"Accesses":  views,
		"ProxyHost": proxyHost,
		"Q":         lq,
	})
}

func (g *GUI) postNewAccess(w http.ResponseWriter, r *http.Request) {
	if g.badForm(w, r) {
		return
	}
	username := strings.TrimSpace(r.Form.Get("username"))
	password := r.Form.Get("password")
	if username == "" || password == "" {
		http.Error(w, "username and password required", http.StatusBadRequest)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "hash error", http.StatusInternalServerError)
		return
	}
	if _, err := g.Store.CreateUser(r.Context(), username, string(hash)); err != nil {
		g.Log.Error("create user", "err", err)
		http.Error(w, "could not create user", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/access", http.StatusFound)
}

func (g *GUI) postEditAccess(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if g.badForm(w, r) {
		return
	}
	username := strings.TrimSpace(r.Form.Get("username"))
	password := r.Form.Get("password")

	var newHash string
	if password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			http.Error(w, "hash error", http.StatusInternalServerError)
			return
		}
		newHash = string(hash)
	}
	if err := g.Store.UpdateUser(r.Context(), id, username, newHash); err != nil {
		g.Log.Error("update user", "err", err)
		http.Error(w, "could not update user", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/access", http.StatusFound)
}

func (g *GUI) postDeleteAccess(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if g.dbError(w, "delete user", g.Store.DeleteUser(r.Context(), id)) {
		return
	}
	http.Redirect(w, r, "/access", http.StatusFound)
}

type logView struct {
	ID           int64
	TSHuman      string
	User         string
	ClientName   string
	Target       string
	Protocol     string
	Outcome      string
	OutcomeTitle string
	BytesInH     string
	BytesOutH    string
	DurationMS   int64
	ConnectMS    int64
}

func outcomeTitle(outcome, reason string) string {
	switch outcome + ":" + reason {
	case "denied:auth":
		return "log.reason.denied.auth"
	case "denied:ratelimit":
		return "log.reason.denied.ratelimit"
	case "failed:no_agent":
		return "log.reason.failed.no_agent"
	case "failed:dial":
		return "log.reason.failed.dial"
	case "failed:reply":
		return "log.reason.failed.reply"
	case "failed:upstream":
		return "log.reason.failed.upstream"
	case "failed:hijack":
		return "log.reason.failed.hijack"
	case "failed:write":
		return "log.reason.failed.write"
	default:
		switch outcome {
		case "ok":
			return ""
		case "denied":
			return "log.reason.denied"
		default:
			return "log.reason.failed"
		}
	}
}

const logsPageSize = 20

func (g *GUI) getGlobalStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data, err := g.domainStatsPage(ctx, r.URL.Query(), 0)
	if g.dbError(w, "domain stats", err) {
		return
	}
	data["Title"] = "Stats"
	data["Active"] = "stats"
	data["User"] = g.adminName(ctx)
	g.render(w, r, "stats.html", data)
}

func (g *GUI) postResetStats(w http.ResponseWriter, r *http.Request) {
	if _, err := g.Store.PurgeAllProxyEvents(r.Context()); err != nil {
		http.Error(w, "reset failed", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/stats", http.StatusSeeOther)
}

func (g *GUI) getLogs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	since := time.Now().Add(-24 * time.Hour)
	q := r.URL.Query()

	filterUserID, _ := strconv.ParseInt(q.Get("user_id"), 10, 64)
	filterClientID, _ := strconv.ParseInt(q.Get("client_id"), 10, 64)
	search := strings.TrimSpace(q.Get("q"))
	lq := parseListQuery(q, storage.ProxyEventSortKeys, "time", "desc", "user_id", "client_id", "q").
		withDescFirst("time", "in", "out", "connect", "duration")
	filter := storage.ProxyEventFilter{
		Since:    since,
		UserID:   filterUserID,
		ClientID: filterClientID,
		Search:   search,
		Sort:     lq.Sort,
		Desc:     lq.Desc(),
	}

	total, err := g.Store.CountProxyEventsFiltered(ctx, filter)
	if g.dbError(w, "count proxy events", err) {
		return
	}
	offset, data := lq.paginate(total, logsPageSize)

	events, err := g.Store.ListProxyEventsFiltered(ctx, filter, logsPageSize, offset)
	if g.dbError(w, "list proxy events", err) {
		return
	}
	views := make([]logView, 0, len(events))
	for _, e := range events {
		views = append(views, logView{
			ID:           e.ID,
			TSHuman:      e.TS.Format("15:04:05"),
			User:         e.Username,
			ClientName:   e.ClientName,
			Target:       e.TargetHost,
			Protocol:     e.Protocol,
			Outcome:      e.Outcome,
			OutcomeTitle: outcomeTitle(e.Outcome, e.OutcomeReason),
			BytesInH:     humanBytes(e.BytesIn),
			BytesOutH:    humanBytes(e.BytesOut),
			DurationMS:   e.DurationMS,
			ConnectMS:    e.ConnectMS,
		})
	}
	clients, err := g.Store.ListClients(ctx)
	if g.dbError(w, "list clients", err) {
		return
	}
	clientOpts := make([]clientOption, 0, len(clients))
	for _, c := range clients {
		clientOpts = append(clientOpts, clientOption{ID: c.ID, Name: c.Name})
	}
	users, err := g.Store.ListUsers(ctx)
	if g.dbError(w, "list users", err) {
		return
	}
	userOpts := make([]clientOption, 0, len(users))
	for _, u := range users {
		userOpts = append(userOpts, clientOption{ID: u.ID, Name: u.Username})
	}

	hasFilter := filterUserID > 0 || filterClientID > 0 || search != ""

	data["Title"] = "Logs"
	data["Active"] = "logs"
	data["User"] = g.adminName(ctx)
	data["Events"] = views
	data["ClientOptions"] = clientOpts
	data["UserOptions"] = userOpts
	data["FilterUserID"] = filterUserID
	data["FilterClientID"] = filterClientID
	data["FilterSearch"] = search
	data["HasFilter"] = hasFilter
	data["Q"] = lq
	g.render(w, r, "logs.html", data)
}

func (g *GUI) render(w http.ResponseWriter, r *http.Request, name string, data map[string]any) {
	v := g.view(r)
	if data == nil {
		data = map[string]any{}
	}
	data["CSRFToken"] = g.ensureCSRF(w, r)
	data["Timezone"] = g.Cfg.Timezone

	var buf bytes.Buffer
	if err := g.tpl.render(&buf, name, v.lang, v.common(data)); err != nil {
		g.Log.Error("render", "name", name, "err", err)
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

func (g *GUI) renderFlash(w http.ResponseWriter, r *http.Request, name, flash, kind, title string) {
	g.render(w, r, name, map[string]any{
		"Title":     title,
		"Flash":     flash,
		"FlashKind": kind,
	})
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func humanBytes(n int64) string {
	const k = 1024
	if n < k {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	f := float64(n) / k
	for _, u := range units {
		if f < k {
			return fmt.Sprintf("%.2f %s", f, u)
		}
		f /= k
	}
	return fmt.Sprintf("%.2f PB", f)
}

func humanDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

func humanSince(t time.Time) string {
	if t.IsZero() || t.Unix() == 0 {
		return "never"
	}
	return humanDuration(time.Since(t)) + " ago"
}
