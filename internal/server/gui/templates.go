package gui

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"

	"github.com/butialabs/proxywi/internal/i18n"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed dist/css/app.min.css
var appCSS string

//go:embed dist/js/app.min.js
var appJS string

//go:embed dist
var staticFS embed.FS

type templates struct {
	layout []byte
	bodies map[string][]byte
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
	bodies := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() || e.Name() == "layout.html" {
			continue
		}
		body, err := templatesFS.ReadFile("templates/" + e.Name())
		if err != nil {
			return nil, err
		}
		bodies[e.Name()] = body
	}
	return &templates{layout: layout, bodies: bodies}, nil
}

func (t *templates) render(w io.Writer, name string, tr i18n.Translator, data any) error {
	body, ok := t.bodies[name]
	if !ok {
		return fmt.Errorf("unknown template %q", name)
	}
	funcs := template.FuncMap{
		"t":      func(key string) string { return tr(key) },
		"tp":     func(key string, params any) string { return expandParams(tr(key), params) },
		"appCSS": func() template.CSS { return template.CSS(appCSS) },
		"appJS":  func() template.JS { return template.JS(appJS) },
		"json": func(v any) (template.JS, error) {
			b, err := json.Marshal(v)
			if err != nil {
				return "", err
			}
			return template.JS(b), nil
		},
	}
	tpl, err := template.New(name).Funcs(funcs).Parse(string(t.layout))
	if err != nil {
		return err
	}
	if _, err := tpl.Parse(string(body)); err != nil {
		return fmt.Errorf("parse %s: %w", name, err)
	}
	return tpl.Execute(w, data)
}

func expandParams(s string, params any) string {
	if params == nil {
		return s
	}
	tpl, err := template.New("inline").Parse(s)
	if err != nil {
		return s
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, params); err != nil {
		return s
	}
	return buf.String()
}
