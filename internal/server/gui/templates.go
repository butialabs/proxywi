package gui

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"html/template"
	"io"
	"sync"

	"github.com/butialabs/proxywi/internal/i18n"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed dist
var staticFS embed.FS

var assetVer = sync.OnceValue(func() string {
	h := fnv.New64a()
	for _, path := range []string{
		"dist/css/app.min.css",
		"dist/js/app.min.js",
		"dist/img/icons.svg",
	} {
		if data, err := staticFS.ReadFile(path); err == nil {
			_, _ = h.Write(data)
		}
	}
	return fmt.Sprintf("%016x", h.Sum64())[:8]
})

type templates struct {
	// byLang[lang][page]
	byLang map[string]map[string]*template.Template
}

func templateFuncs(tr i18n.Translator) template.FuncMap {
	return template.FuncMap{
		"t":   func(key string) string { return tr(key) },
		"tp":  func(key string, params any) string { return expandParams(tr(key), params) },
		"ver": assetVer,
		"dict": func(kv ...any) (map[string]any, error) {
			if len(kv)%2 != 0 {
				return nil, fmt.Errorf("dict: odd number of arguments")
			}
			m := make(map[string]any, len(kv)/2)
			for i := 0; i < len(kv); i += 2 {
				k, ok := kv[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict: key %v is not a string", kv[i])
				}
				m[k] = kv[i+1]
			}
			return m, nil
		},
		"json": func(v any) (template.JS, error) {
			b, err := json.Marshal(v)
			if err != nil {
				return "", err
			}
			return template.JS(b), nil
		},
	}
}

func loadTemplates() (*templates, error) {
	layout, err := templatesFS.ReadFile("templates/layout.html")
	if err != nil {
		return nil, fmt.Errorf("read layout: %w", err)
	}
	entries, err := templatesFS.ReadDir("templates")
	if err != nil {
		return nil, err
	}
	bodies := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || e.Name() == "layout.html" {
			continue
		}
		body, err := templatesFS.ReadFile("templates/" + e.Name())
		if err != nil {
			return nil, err
		}
		bodies[e.Name()] = string(body)
	}

	langs := []string{i18n.DefaultLang}
	for _, l := range i18n.Available() {
		if l.Tag != i18n.DefaultLang {
			langs = append(langs, l.Tag)
		}
	}

	t := &templates{byLang: make(map[string]map[string]*template.Template, len(langs))}
	layoutText := string(layout)
	for _, lang := range langs {
		funcs := templateFuncs(i18n.For(lang, ""))
		pages := make(map[string]*template.Template, len(bodies))
		for name, body := range bodies {
			tpl, err := template.New(name).Funcs(funcs).Parse(layoutText)
			if err != nil {
				return nil, fmt.Errorf("parse layout for %s: %w", lang, err)
			}
			if _, err := tpl.Parse(body); err != nil {
				return nil, fmt.Errorf("parse %s for %s: %w", name, lang, err)
			}
			pages[name] = tpl
		}
		t.byLang[lang] = pages
	}
	return t, nil
}

func (t *templates) render(w io.Writer, name, lang string, data any) error {
	pages, ok := t.byLang[lang]
	if !ok {
		pages = t.byLang[i18n.DefaultLang]
	}
	tpl, ok := pages[name]
	if !ok {
		return fmt.Errorf("unknown template %q", name)
	}
	return tpl.Execute(w, data)
}

var paramTemplates sync.Map

func expandParams(s string, params any) string {
	if params == nil {
		return s
	}
	var tpl *template.Template
	if cached, ok := paramTemplates.Load(s); ok {
		tpl, _ = cached.(*template.Template)
	} else {
		parsed, err := template.New("inline").Parse(s)
		if err != nil {
			return s
		}
		paramTemplates.Store(s, parsed)
		tpl = parsed
	}
	if tpl == nil {
		return s
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, params); err != nil {
		return s
	}
	return buf.String()
}
