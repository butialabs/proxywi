package gui

import (
	"io"
	"strings"
	"testing"
)

func TestLoadTemplates_ParsesEveryPageForEveryLanguage(t *testing.T) {
	tpl, err := loadTemplates()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	if len(tpl.byLang) < 2 {
		t.Fatalf("expected at least 2 languages, got %d", len(tpl.byLang))
	}
	for lang, pages := range tpl.byLang {
		if len(pages) == 0 {
			t.Fatalf("language %q has no pages", lang)
		}
		for name := range pages {
			var sb strings.Builder
			if err := tpl.render(&sb, name, lang, pageData()); err != nil {
				t.Fatalf("render %s/%s: %v", lang, name, err)
			}
			if sb.Len() == 0 {
				t.Fatalf("render %s/%s produced no output", lang, name)
			}
		}
	}
}

func TestRender_UsesPerLanguageTranslator(t *testing.T) {
	tpl, err := loadTemplates()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	var en, pt strings.Builder
	if err := tpl.render(&en, "login.html", "en", pageData()); err != nil {
		t.Fatalf("render en: %v", err)
	}
	if err := tpl.render(&pt, "login.html", "pt-br", pageData()); err != nil {
		t.Fatalf("render pt-br: %v", err)
	}
	if en.String() == pt.String() {
		t.Fatal("en and pt-br rendered identically; the translator is not per language")
	}
}

func TestRender_UnknownTemplateIsAnError(t *testing.T) {
	tpl, err := loadTemplates()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	if err := tpl.render(io.Discard, "nope.html", "en", pageData()); err == nil {
		t.Fatal("unknown template should fail")
	}
}

func TestRender_UnknownLanguageFallsBack(t *testing.T) {
	tpl, err := loadTemplates()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	var sb strings.Builder
	if err := tpl.render(&sb, "login.html", "xx-yy", pageData()); err != nil {
		t.Fatalf("render fallback: %v", err)
	}
	if sb.Len() == 0 {
		t.Fatal("fallback render produced no output")
	}
}

func pageData() map[string]any {
	return map[string]any{
		"Title":      "T",
		"CSRFToken":  "tok",
		"Timezone":   "UTC",
		"Lang":       "en",
		"Langs":      nil,
		"Page":       1,
		"TotalPages": 1,
		"Total":      0,
		"PrevPage":   0,
		"NextPage":   2,
		"HasPrev":    false,
		"HasNext":    false,
		"Period":     "week",
	}
}

func BenchmarkRenderLoginPage(b *testing.B) {
	tpl, err := loadTemplates()
	if err != nil {
		b.Fatalf("load templates: %v", err)
	}
	data := pageData()
	b.ReportAllocs()
	for b.Loop() {
		if err := tpl.render(io.Discard, "login.html", "en", data); err != nil {
			b.Fatalf("render: %v", err)
		}
	}
}
