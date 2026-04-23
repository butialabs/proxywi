package gui

import (
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/butialabs/proxywi/internal/config"
	"github.com/butialabs/proxywi/internal/server"
	"github.com/butialabs/proxywi/internal/storage"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
)

type GUI struct {
	Store    *storage.Store
	Registry *server.Registry
	Hub      *server.Hub
	Cfg      config.Server
	Log      *slog.Logger

	tpl *templates
}

func New(store *storage.Store, reg *server.Registry, hub *server.Hub, cfg config.Server, log *slog.Logger) (*GUI, error) {
	t, err := loadTemplates()
	if err != nil {
		return nil, err
	}
	return &GUI{Store: store, Registry: reg, Hub: hub, Cfg: cfg, Log: log, tpl: t}, nil
}

func (g *GUI) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.Recoverer)
	r.Use(noIndexHeader)

	r.Get("/robots.txt", robotsTxt)
	mountStaticAssets(r)

	r.Group(func(r chi.Router) {
		r.Use(g.setupGate)

		r.Get("/setup", g.getSetup)
		r.Post("/setup", g.postSetup)

		r.Get("/login", g.getLogin)
		r.Post("/login", g.postLogin)
		r.Post("/logout", g.postLogout)

		r.Group(func(r chi.Router) {
			r.Use(g.requireAuth)
			r.Get("/", g.getDashboard)
			r.Get("/clients", g.getClients)
			r.Post("/clients/new", g.postNewClient)
			r.Post("/clients/{id}/edit", g.postEditClient)
			r.Post("/clients/{id}/regenerate", g.postRegenerateClient)
			r.Get("/clients/{id}/compose", g.getClientCompose)
			r.Post("/clients/{id}/delete", g.postDeleteClient)
			r.Get("/access", g.getAccess)
			r.Post("/access/new", g.postNewAccess)
			r.Post("/access/{id}/edit", g.postEditAccess)
			r.Post("/access/{id}/delete", g.postDeleteAccess)
			r.Get("/security", g.getSecurity)
			r.Post("/security/ban", g.postBan)
			r.Post("/security/unban", g.postUnban)
			r.Get("/logs", g.getLogs)
			r.Get("/events/dashboard", g.eventsDashboard)
			r.Get("/events/logs", g.eventsLogs)
		})
	})

	return r
}

func (g *GUI) setupGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, err := g.Store.CountAdmins(r.Context())
		if err != nil {
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		if n == 0 {
			if r.URL.Path == "/setup" || strings.HasPrefix(r.URL.Path, "/setup/") {
				next.ServeHTTP(w, r)
				return
			}
			http.Redirect(w, r, "/setup", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func mountStaticAssets(r chi.Router) {
	sub, err := fs.Sub(staticFS, "dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.StripPrefix("/dist/", cacheableFileServer(http.FS(sub)))
	r.Handle("/dist/*", fileServer)

	r.Get("/favicon.ico", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/dist/img/kiwi.svg", http.StatusMovedPermanently)
	})
}

func cacheableFileServer(h http.FileSystem) http.Handler {
	fs := http.FileServer(h)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		fs.ServeHTTP(w, req)
	})
}

func noIndexHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive, nosnippet, noimageindex, notranslate, nocache")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func robotsTxt(w http.ResponseWriter, r *http.Request) {
	const body = `# This is a private admin interface. Do not index, archive, or use for
# AI/LLM training. The specific user-agents below repeat the catch-all
# because several crawlers only honor directives under their own name.

User-agent: *
Disallow: /

User-agent: GPTBot
Disallow: /

User-agent: ChatGPT-User
Disallow: /

User-agent: OAI-SearchBot
Disallow: /

User-agent: ClaudeBot
Disallow: /

User-agent: Claude-Web
Disallow: /

User-agent: anthropic-ai
Disallow: /

User-agent: PerplexityBot
Disallow: /

User-agent: Perplexity-User
Disallow: /

User-agent: CCBot
Disallow: /

User-agent: Google-Extended
Disallow: /

User-agent: Googlebot
Disallow: /

User-agent: Googlebot-News
Disallow: /

User-agent: Googlebot-Image
Disallow: /

User-agent: AdsBot-Google
Disallow: /

User-agent: Bingbot
Disallow: /

User-agent: Slurp
Disallow: /

User-agent: DuckDuckBot
Disallow: /

User-agent: Baiduspider
Disallow: /

User-agent: YandexBot
Disallow: /

User-agent: Applebot
Disallow: /

User-agent: Applebot-Extended
Disallow: /

User-agent: Bytespider
Disallow: /

User-agent: Amazonbot
Disallow: /

User-agent: Meta-ExternalAgent
Disallow: /

User-agent: FacebookBot
Disallow: /

User-agent: cohere-ai
Disallow: /

User-agent: Diffbot
Disallow: /

User-agent: ImagesiftBot
Disallow: /

User-agent: Omgilibot
Disallow: /

User-agent: YouBot
Disallow: /

User-agent: ia_archiver
Disallow: /

User-agent: archive.org_bot
Disallow: /
`
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write([]byte(body))
}
