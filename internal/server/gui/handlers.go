package gui

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
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
	row := g.Store.DB().QueryRowContext(ctx, `SELECT username FROM admins WHERE id = ?`, id)
	var u string
	_ = row.Scan(&u)
	return u
}

func (g *GUI) getLogin(w http.ResponseWriter, r *http.Request) {
	g.render(w, r, "login.html", map[string]any{"Title": "Sign in"})
}

func (g *GUI) postLogin(w http.ResponseWriter, r *http.Request) {
	v := g.view(r)
	_ = r.ParseForm()
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
		_ = g.Store.DeleteSession(r.Context(), c.Value)
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

	allClients, _ := g.Store.ListClients(ctx)

	since := time.Now().Add(-p.duration)
	mf := storage.MetricsFilter{Since: since, BucketSeconds: int64(p.bucket.Seconds())}
	samples, _ := g.Store.Metrics(ctx, mf)

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

type clientView struct {
	ID            int64
	Name          string
	AgentVersion  string
	Online        bool
	LastSeenHuman string
}

const composeTokenPlaceholder = "<paste-your-token-here>"

func (g *GUI) getClients(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	v := g.view(r)
	list, _ := g.Store.ListClients(ctx)
	views := make([]clientView, 0, len(list))
	for _, c := range list {
		_, online := g.Registry.Get(c.ID)
		views = append(views, clientView{
			ID:            c.ID,
			Name:          c.Name,
			AgentVersion:  c.AgentVersion,
			Online:        online,
			LastSeenHuman: humanSince(c.LastSeen),
		})
	}
	data := map[string]any{
		"Title":   "Clients",
		"Active":  "clients",
		"User":    g.adminName(ctx),
		"Clients": views,
		"ClientsData": map[string]string{
			"confirmTpl": v.tr("clients.confirm_regenerate"),
		},
	}
	
	tok := g.getFlashToken(w, r)
	if tok != "" {
		if id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64); err == nil {
			if c, _ := g.Store.ClientByID(ctx, id); c != nil {
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
	id, err := g.Store.CreateClient(r.Context(), name, string(hash), storage.TokenIDFromToken(token))
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
	if err := g.Store.UpdateClientToken(r.Context(), id, string(hash), storage.TokenIDFromToken(token)); err != nil {
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
	_ = g.Store.DeleteClient(r.Context(), id)
	http.Redirect(w, r, "/clients", http.StatusFound)
}

type accessView struct {
	ID            int64
	Username      string
	LastUsedHuman string
	UsedCount     int
}

func (g *GUI) getAccess(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	list, _ := g.Store.ListUsers(ctx)
	usage, _ := g.Store.UserUsageStats(ctx)

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
		})
	}

	proxyHost := g.Cfg.MainDomain
	if proxyHost == "" {
		proxyHost = g.Cfg.ProxyDomain
	}

	g.render(w, r, "access.html", map[string]any{
		"Title":     "Proxy Access",
		"Active":    "access",
		"User":      g.adminName(ctx),
		"Accesses":  views,
		"ProxyHost": proxyHost,
	})
}

func (g *GUI) postNewAccess(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
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
	_ = r.ParseForm()
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
	_ = g.Store.DeleteUser(r.Context(), id)
	http.Redirect(w, r, "/access", http.StatusFound)
}

type logView struct {
	ID         int64
	TSHuman    string
	User       string
	ClientName string
	Target     string
	Protocol   string
	Outcome    string
	BytesInH   string
	BytesOutH  string
	DurationMS int64
}

const logsPageSize = 20

func anonymizeTarget(target string) string {
	if target == "" {
		return ""
	}
	host, port, hasPort := splitTargetHostPort(target)
	if strings.HasPrefix(host, "[") {
		out := "[****]"
		if hasPort {
			out += ":" + port
		}
		return out
	}
	labels := strings.Split(host, ".")
	for i, l := range labels {
		n := len(l)
		switch {
		case n <= 2:
			labels[i] = strings.Repeat("*", n)
		case n == 3:
			labels[i] = "*" + l[n-2:]
		default:
			labels[i] = l[:3] + strings.Repeat("*", n-3)
		}
	}
	out := strings.Join(labels, ".")
	if hasPort {
		out += ":" + port
	}
	return out
}

func splitTargetHostPort(target string) (host, port string, hasPort bool) {
	if strings.HasPrefix(target, "[") {
		if i := strings.LastIndex(target, "]:"); i > 0 {
			return target[:i+1], target[i+2:], true
		}
		return target, "", false
	}
	if i := strings.LastIndex(target, ":"); i > 0 {
		portStr := target[i+1:]
		if _, err := strconv.Atoi(portStr); err == nil {
			return target[:i], portStr, true
		}
	}
	return target, "", false
}

func (g *GUI) getLogs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	since := time.Now().Add(-24 * time.Hour)
	q := r.URL.Query()

	filterUserID, _ := strconv.ParseInt(q.Get("user_id"), 10, 64)
	filterClientID, _ := strconv.ParseInt(q.Get("client_id"), 10, 64)
	search := strings.TrimSpace(q.Get("q"))
	filter := storage.ProxyEventFilter{
		Since:    since,
		UserID:   filterUserID,
		ClientID: filterClientID,
		Search:   search,
	}

	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * logsPageSize

	total, _ := g.Store.CountProxyEventsFiltered(ctx, filter)
	totalPages := (total + logsPageSize - 1) / logsPageSize
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
		offset = (page - 1) * logsPageSize
	}

	events, _ := g.Store.ListProxyEventsFiltered(ctx, filter, logsPageSize, offset)
	views := make([]logView, 0, len(events))
	for _, e := range events {
		views = append(views, logView{
			ID:         e.ID,
			TSHuman:    e.TS.Format("15:04:05"),
			User:       e.Username,
			ClientName: e.ClientName,
			Target:     anonymizeTarget(e.TargetHost),
			Protocol:   e.Protocol,
			Outcome:    e.Outcome,
			BytesInH:   humanBytes(e.BytesIn),
			BytesOutH:  humanBytes(e.BytesOut),
			DurationMS: e.DurationMS,
		})
	}
	clients, _ := g.Store.ListClients(ctx)
	clientOpts := make([]clientOption, 0, len(clients))
	for _, c := range clients {
		clientOpts = append(clientOpts, clientOption{ID: c.ID, Name: c.Name})
	}
	users, _ := g.Store.ListUsers(ctx)
	userOpts := make([]clientOption, 0, len(users))
	for _, u := range users {
		userOpts = append(userOpts, clientOption{ID: u.ID, Name: u.Username})
	}

	// Query string carrying the active filters, appended to pagination links.
	filterVals := url.Values{}
	if filterUserID > 0 {
		filterVals.Set("user_id", strconv.FormatInt(filterUserID, 10))
	}
	if filterClientID > 0 {
		filterVals.Set("client_id", strconv.FormatInt(filterClientID, 10))
	}
	if search != "" {
		filterVals.Set("q", search)
	}
	filterQS := ""
	if enc := filterVals.Encode(); enc != "" {
		filterQS = "&" + enc
	}
	hasFilter := filterUserID > 0 || filterClientID > 0 || search != ""

	g.render(w, r, "logs.html", map[string]any{
		"Title":          "Logs",
		"Active":         "logs",
		"User":           g.adminName(ctx),
		"Events":         views,
		"Page":           page,
		"TotalPages":     totalPages,
		"Total":          total,
		"PrevPage":       page - 1,
		"NextPage":       page + 1,
		"HasPrev":        page > 1,
		"HasNext":        page < totalPages,
		"ClientOptions":  clientOpts,
		"UserOptions":    userOpts,
		"FilterUserID":   filterUserID,
		"FilterClientID": filterClientID,
		"FilterSearch":   search,
		"FilterQS":       template.HTML(filterQS),
		"HasFilter":      hasFilter,
	})
}

func (g *GUI) render(w http.ResponseWriter, r *http.Request, name string, data map[string]any) {
	v := g.view(r)
	if data == nil {
		data = map[string]any{}
	}
	data["CSRFToken"] = g.ensureCSRF(w, r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := g.tpl.render(w, name, v.tr, v.common(data)); err != nil {
		g.Log.Error("render", "name", name, "err", err)
		http.Error(w, "render error", http.StatusInternalServerError)
	}
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

