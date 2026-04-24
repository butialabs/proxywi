package gui

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/butialabs/proxywi/internal/i18n"
	"github.com/butialabs/proxywi/internal/server"
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

func (g *GUI) getSetup(w http.ResponseWriter, r *http.Request) {
	if n, _ := g.Store.CountAdmins(r.Context()); n > 0 {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	g.render(w, r, "setup.html", map[string]any{"Title": "Setup"})
}

func (g *GUI) postSetup(w http.ResponseWriter, r *http.Request) {
	v := g.view(r)
	if n, _ := g.Store.CountAdmins(r.Context()); n > 0 {
		g.renderFlash(w, r, "login.html", v.tr("setup.already_configured"), "warning", "Setup")
		return
	}
	_ = r.ParseForm()
	username := strings.TrimSpace(r.Form.Get("username"))
	email := strings.TrimSpace(r.Form.Get("email"))
	password := r.Form.Get("password")
	confirm := r.Form.Get("password_confirm")

	form := map[string]any{
		"Title":        "Setup",
		"FormUsername": username,
		"FormEmail":    email,
	}
	if username == "" || email == "" {
		form["Flash"] = v.tr("setup.title")
		form["FlashKind"] = "danger"
		g.render(w, r, "setup.html", form)
		return
	}
	if len(password) < 8 {
		form["Flash"] = v.tr("setup.password_too_short")
		form["FlashKind"] = "danger"
		g.render(w, r, "setup.html", form)
		return
	}
	if password != confirm {
		form["Flash"] = v.tr("setup.password_mismatch")
		form["FlashKind"] = "danger"
		g.render(w, r, "setup.html", form)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "hash error", http.StatusInternalServerError)
		return
	}
	if err := g.Store.CreateAdmin(r.Context(), username, email, string(hash)); err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	admin, _ := g.Store.AdminByUsername(r.Context(), username)
	if admin == nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	sid, err := g.Store.CreateSession(r.Context(), admin.ID, 24*time.Hour)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	g.setSession(w, sid)
	http.Redirect(w, r, "/", http.StatusFound)
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

type agentView struct {
	Name          string
	RemoteIP      string
	SinceHuman    string
	ActiveConns   int64
	BytesInHuman  string
	BytesOutHuman string
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
	filterClientID, _ := strconv.ParseInt(q.Get("client_id"), 10, 64)

	since := time.Now().Add(-p.duration)
	samples, _ := g.Store.Metrics(ctx, storage.MetricsFilter{
		Since:         since,
		ClientID:      filterClientID,
		BucketSeconds: int64(p.bucket.Seconds()),
	})

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
	if filterClientID > 0 {
		filtered := onlineAgents[:0]
		for _, a := range onlineAgents {
			if a.ID == filterClientID {
				filtered = append(filtered, a)
			}
		}
		onlineAgents = filtered
	}
	allClients, _ := g.Store.ListClients(ctx)

	var totalIn, totalOut, activeConns int64
	views := make([]agentView, 0, len(onlineAgents))
	for _, a := range onlineAgents {
		activeConns += a.ActiveConns()
		views = append(views, agentView{
			Name:          a.Name,
			RemoteIP:      a.RemoteIP,
			SinceHuman:    humanDuration(time.Since(a.ConnectAt)),
			ActiveConns:   a.ActiveConns(),
			BytesInHuman:  humanBytes(a.BytesIn()),
			BytesOutHuman: humanBytes(a.BytesOut()),
		})
	}
	for _, s := range samples {
		totalIn += s.BytesIn
		totalOut += s.BytesOut
	}

	opts := make([]clientOption, 0, len(allClients))
	for _, c := range allClients {
		opts = append(opts, clientOption{ID: c.ID, Name: c.Name})
	}
	pos := make([]periodOption, 0, len(periodOrder))
	for _, k := range periodOrder {
		pos = append(pos, periodOption{Value: k, Label: v.tr(periodDefs[k].labelKey)})
	}

	totalClients := len(allClients)
	if filterClientID > 0 {
		totalClients = 1
	}

	g.render(w, r, "dashboard.html", map[string]any{
		"Title":          "Dashboard",
		"Active":         "dashboard",
		"User":           g.adminName(ctx),
		"OnlineCount":    len(views),
		"TotalClients":   totalClients,
		"ActiveConns":    activeConns,
		"BytesInHuman":   humanBytes(totalIn),
		"BytesOutHuman":  humanBytes(totalOut),
		"Online":         views,
		"ClientOptions":  opts,
		"PeriodOptions":  pos,
		"FilterClientID": filterClientID,
		"FilterPeriod":   periodKey,
		"PeriodHuman":    v.tr(p.labelKey),
		"DashboardData": map[string]any{
			"labels":      labels,
			"dataIn":      in,
			"dataOut":     out,
			"onlineCount": len(views),
			"tr": map[string]string{
				"in":           v.tr("dashboard.chart_in"),
				"out":          v.tr("dashboard.chart_out"),
				"connected":    v.tr("logs.live_connected"),
				"disconnected": v.tr("logs.live_disconnected"),
			},
		},
	})
}

type clientView struct {
	ID            int64
	Name          string
	Online        bool
	CurrentIP     string
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
			Online:        online,
			CurrentIP:     server.MaskIP(c.CurrentIP),
			LastSeenHuman: humanSince(c.LastSeen),
		})
	}
	data := map[string]any{
		"Title":   "Clients",
		"Active":  "clients",
		"User":    g.adminName(ctx),
		"Clients": views,
		"ClientsData": map[string]string{
			"titleTpl":   v.tr("clients.compose_view_title"),
			"confirmTpl": v.tr("clients.confirm_regenerate"),
		},
	}
	
	if tok := r.URL.Query().Get("token"); tok != "" {
		if id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64); err == nil {
			if c, _ := g.Store.ClientByID(ctx, id); c != nil {
				data["NewCompose"] = buildCompose(c.Name, controlURL(r), tok)
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

func buildCompose(clientName, controlURL, token string) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "services:\n")
	fmt.Fprintf(&b, "  proxywi-client:\n")
	fmt.Fprintf(&b, "    image: ghcr.io/butialabs/proxywi-client:latest\n")
	fmt.Fprintf(&b, "    restart: unless-stopped\n")
	fmt.Fprintf(&b, "    environment:\n")
	fmt.Fprintf(&b, "      PROXYWI_SERVER: %q\n", controlURL)
	fmt.Fprintf(&b, "      PROXYWI_TOKEN:  %q\n", token)
	fmt.Fprintf(&b, "      PROXYWI_CLIENT_NAME: %q\n", clientName)
	return b.String()
}

func (g *GUI) postNewClient(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	name := strings.TrimSpace(r.Form.Get("name"))
	if name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	token := randHex(32)
	hash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "hash error", http.StatusInternalServerError)
		return
	}
	id, err := g.Store.CreateClient(r.Context(), name, string(hash))
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/clients?token=%s&id=%d", token, id), http.StatusFound)
}

func (g *GUI) postEditClient(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	_ = r.ParseForm()
	name := strings.TrimSpace(r.Form.Get("name"))
	if name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	if err := g.Store.UpdateClientName(r.Context(), id, name); err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/clients", http.StatusFound)
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
	if err := g.Store.UpdateClientToken(r.Context(), id, string(hash)); err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	g.Registry.Disconnect(id)
	http.Redirect(w, r, fmt.Sprintf("/clients?token=%s&id=%d", token, id), http.StatusFound)
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
	_, _ = w.Write([]byte(buildCompose(c.Name, controlURL(r), composeTokenPlaceholder)))
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
	ID                 int64
	Username           string
	AllowedSourceCIDRs []string
	CIDRsCSV           string
	CreatedHuman       string
	AllClients         bool
	AllowedClientIDs   []int64
	ClientIDsCSV       string
	AllowedClientNames []string
}

func (g *GUI) getAccess(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	list, _ := g.Store.ListUsers(ctx)
	clients, _ := g.Store.ListClients(ctx)

	clientByID := make(map[int64]string, len(clients))
	for _, c := range clients {
		clientByID[c.ID] = c.Name
	}

	views := make([]accessView, 0, len(list))
	for _, u := range list {
		ids := u.AllowedClientIDs
		idStrs := make([]string, 0, len(ids))
		names := make([]string, 0, len(ids))
		for _, id := range ids {
			idStrs = append(idStrs, strconv.FormatInt(id, 10))
			if n, ok := clientByID[id]; ok {
				names = append(names, n)
			}
		}
		views = append(views, accessView{
			ID:                 u.ID,
			Username:           u.Username,
			AllowedSourceCIDRs: u.AllowedSourceCIDRs,
			CIDRsCSV:           strings.Join(u.AllowedSourceCIDRs, ","),
			CreatedHuman:       u.CreatedAt.Format("2006-01-02"),
			AllClients:         len(ids) == 0,
			AllowedClientIDs:   ids,
			ClientIDsCSV:       strings.Join(idStrs, ","),
			AllowedClientNames: names,
		})
	}

	clientOpts := make([]clientOption, 0, len(clients))
	for _, c := range clients {
		clientOpts = append(clientOpts, clientOption{ID: c.ID, Name: c.Name})
	}

	g.render(w, r, "access.html", map[string]any{
		"Title":         "Proxy Access",
		"Active":        "access",
		"User":          g.adminName(ctx),
		"Accesses":      views,
		"ClientOptions": clientOpts,
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
	cidrs := splitCSV(r.Form.Get("cidrs"))
	clientIDs := parseClientIDs(r.Form["client_ids"])
	if _, err := g.Store.CreateUser(r.Context(), username, string(hash), cidrs, clientIDs); err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
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
	cidrs := splitCSV(r.Form.Get("cidrs"))
	clientIDs := parseClientIDs(r.Form["client_ids"])

	var newHash string
	if password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			http.Error(w, "hash error", http.StatusInternalServerError)
			return
		}
		newHash = string(hash)
	}
	if err := g.Store.UpdateUser(r.Context(), id, username, newHash, cidrs, true, clientIDs, true); err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/access", http.StatusFound)
}

func parseClientIDs(raw []string) []int64 {
	out := make([]int64, 0, len(raw))
	seen := map[int64]bool{}
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil || id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
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
	SourceIP   string
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

func (g *GUI) getLogs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	since := time.Now().Add(-24 * time.Hour)

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * logsPageSize

	total, _ := g.Store.CountProxyEvents(ctx, since)
	totalPages := (total + logsPageSize - 1) / logsPageSize
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
		offset = (page - 1) * logsPageSize
	}

	events, _ := g.Store.ListProxyEventsPage(ctx, since, logsPageSize, offset)
	views := make([]logView, 0, len(events))
	for _, e := range events {
		views = append(views, logView{
			ID:         e.ID,
			TSHuman:    e.TS.Format("15:04:05"),
			SourceIP:   e.SourceIP,
			User:       e.Username,
			ClientName: e.ClientName,
			Target:     e.TargetHost,
			Protocol:   e.Protocol,
			Outcome:    e.Outcome,
			BytesInH:   humanBytes(e.BytesIn),
			BytesOutH:  humanBytes(e.BytesOut),
			DurationMS: e.DurationMS,
		})
	}
	v := g.view(r)
	g.render(w, r, "logs.html", map[string]any{
		"Title":      "Logs",
		"Active":     "logs",
		"User":       g.adminName(ctx),
		"Events":     views,
		"Page":       page,
		"TotalPages": totalPages,
		"Total":      total,
		"PrevPage":   page - 1,
		"NextPage":   page + 1,
		"HasPrev":    page > 1,
		"HasNext":    page < totalPages,
		"LogsData": map[string]any{
			"onFirstPage": page <= 1,
			"pageSize":    logsPageSize,
			"tr": map[string]string{
				"connected":    v.tr("logs.live_connected"),
				"disconnected": v.tr("logs.live_disconnected"),
			},
		},
	})
}

type banView struct {
	SourceIP     string
	Reason       string
	FailureCount int
	ExpiresHuman string
}

func (g *GUI) getSecurity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	bans, _ := g.Store.ListBans(ctx, true)
	views := make([]banView, 0, len(bans))
	for _, b := range bans {
		views = append(views, banView{
			SourceIP:     b.SourceIP,
			Reason:       b.Reason,
			FailureCount: b.FailureCount,
			ExpiresHuman: humanUntil(b.BannedUntil),
		})
	}
	g.render(w, r, "security.html", map[string]any{
		"Title":      "Security",
		"Active":     "security",
		"User":       g.adminName(ctx),
		"ActiveBans": views,
	})
}

func (g *GUI) postBan(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	ip := strings.TrimSpace(r.Form.Get("ip"))
	hours, _ := strconv.Atoi(r.Form.Get("hours"))
	if hours <= 0 {
		hours = 24
	}
	reason := r.Form.Get("reason")
	if reason == "" {
		reason = "manual"
	}
	if ip == "" {
		http.Error(w, "ip required", http.StatusBadRequest)
		return
	}
	_ = g.Store.UpsertBan(r.Context(), ip, time.Now().Add(time.Duration(hours)*time.Hour), reason, 0)
	http.Redirect(w, r, "/security", http.StatusFound)
}

func (g *GUI) postUnban(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	ip := strings.TrimSpace(r.Form.Get("ip"))
	if ip != "" {
		_ = g.Store.UnbanIP(r.Context(), ip)
	}
	http.Redirect(w, r, "/security", http.StatusFound)
}

func (g *GUI) render(w http.ResponseWriter, r *http.Request, name string, data map[string]any) {
	v := g.view(r)
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

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
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

func humanUntil(t time.Time) string {
	d := time.Until(t)
	if d <= 0 {
		return "expired"
	}
	return "in " + humanDuration(d)
}
