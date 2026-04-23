// To add a language, drop a locales/<tag>.json file, locales are embedded at build time.
// Missing keys and unknown locales fall back to English.
package i18n

import (
	"embed"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

//go:embed locales/*.json
var localesFS embed.FS

const DefaultLang = "en"

type bundle struct {
	lang    string
	strings map[string]string
}

type LangInfo struct {
	Tag  string
	Name string
}

var (
	once      sync.Once
	bundles   map[string]*bundle
	fallback  *bundle
	available []LangInfo
)

func load() {
	bundles = map[string]*bundle{}
	entries, err := localesFS.ReadDir("locales")
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := localesFS.ReadFile("locales/" + e.Name())
		if err != nil {
			continue
		}
		m := map[string]string{}
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		tag := strings.TrimSuffix(e.Name(), ".json")
		b := &bundle{lang: tag, strings: m}
		bundles[tag] = b
		name := m["lang.name"]
		if name == "" {
			name = tag
		}
		available = append(available, LangInfo{Tag: tag, Name: name})
		if tag == DefaultLang {
			fallback = b
		}
	}
	if fallback == nil {
		fallback = &bundle{lang: DefaultLang, strings: map[string]string{}}
	}
}

func Available() []LangInfo {
	once.Do(load)
	return available
}

type Translator func(key string) string

func For(override, acceptLang string) Translator {
	once.Do(load)
	b := pick(override, acceptLang)
	return func(key string) string {
		if v, ok := b.strings[key]; ok && v != "" {
			return v
		}
		if fallback != nil {
			if v, ok := fallback.strings[key]; ok && v != "" {
				return v
			}
		}
		return key
	}
}

func FromRequest(r *http.Request) (string, Translator) {
	once.Do(load)
	var override string
	if c, err := r.Cookie("lang"); err == nil {
		override = c.Value
	}
	b := pick(override, r.Header.Get("Accept-Language"))
	return b.lang, For(override, r.Header.Get("Accept-Language"))
}

func pick(override, acceptLang string) *bundle {
	if override != "" {
		if b, ok := bundles[normalize(override)]; ok {
			return b
		}
	}
	for _, tag := range parseAcceptLang(acceptLang) {
		if b, ok := bundles[tag]; ok {
			return b
		}
	}
	return fallback
}

func parseAcceptLang(h string) []string {
	if h == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(h, ",") {
		tag := strings.TrimSpace(part)
		if i := strings.IndexByte(tag, ';'); i >= 0 {
			tag = tag[:i]
		}
		tag = normalize(tag)
		if tag == "" {
			continue
		}
		out = append(out, tag)
		if i := strings.IndexByte(tag, '-'); i > 0 {
			out = append(out, tag[:i])
		}
	}
	return out
}

func normalize(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
