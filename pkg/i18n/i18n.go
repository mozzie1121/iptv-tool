package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/text/language"
)

// localeData holds parsed locale data for a single language.
type localeData struct {
	backend  map[string]string // flattened keys like "error.invalid_id"
	frontend json.RawMessage   // raw JSON for serving to the frontend
}

var (
	locales            = make(map[string]*localeData) // keyed by language code (e.g. "en", "zh")
	supportedTags      []language.Tag
	supportedLanguages []string
	matcher            language.Matcher
)

// Init loads all *.json locale files from the embedded filesystem.
// It must be called once at application startup before any other function.
func Init(fs embed.FS) {
	entries, err := fs.ReadDir(".")
	if err != nil {
		slog.Error("failed to read locale directory", "error", err)
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		lang := strings.TrimSuffix(entry.Name(), ".json")

		data, err := fs.ReadFile(entry.Name())
		if err != nil {
			slog.Error("failed to read locale file", "file", entry.Name(), "error", err)
			continue
		}

		var raw struct {
			Frontend json.RawMessage              `json:"frontend"`
			Backend  map[string]map[string]string `json:"backend"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			slog.Error("failed to parse locale file", "file", entry.Name(), "error", err)
			continue
		}

		ld := &localeData{
			backend:  make(map[string]string),
			frontend: raw.Frontend,
		}

		// Flatten backend sections: {"error": {"invalid_id": "..."}} -> "error.invalid_id"
		for section, entries := range raw.Backend {
			for key, value := range entries {
				ld.backend[section+"."+key] = value
			}
		}

		locales[lang] = ld

		supportedLanguages = append(supportedLanguages, lang)
	}

	sort.Strings(supportedLanguages)

	// Build tags in the same order as the sorted supportedLanguages
	// so matcher index aligns with supportedLanguages index.
	supportedTags = make([]language.Tag, 0, len(supportedLanguages))
	for _, lang := range supportedLanguages {
		tag, err := language.Parse(lang)
		if err != nil {
			slog.Error("failed to parse language tag", "lang", lang, "error", err)
			continue
		}
		supportedTags = append(supportedTags, tag)
	}
	matcher = language.NewMatcher(supportedTags)

	slog.Info("i18n initialized", "languages", supportedLanguages)
}

// T translates a key for the given language. Optional args are passed to fmt.Sprintf.
// Falls back to "en" if the language is not found, and to the key itself if the key is not found.
func T(lang, key string, args ...interface{}) string {
	ld, ok := locales[lang]
	if !ok {
		ld, ok = locales["en"]
		if !ok {
			return formatOrKey(key, args)
		}
	}

	val, ok := ld.backend[key]
	if !ok {
		// Try English fallback if not already using it
		if lang != "en" {
			if enLD, ok := locales["en"]; ok {
				if val, ok = enLD.backend[key]; ok {
					return formatOrKey(val, args)
				}
			}
		}
		return formatOrKey(key, args)
	}

	return formatOrKey(val, args)
}

// formatOrKey applies fmt.Sprintf if args are provided, otherwise returns the string as-is.
func formatOrKey(s string, args []interface{}) string {
	if len(args) > 0 {
		return fmt.Sprintf(s, args...)
	}
	return s
}

// GetFrontendLocale returns the raw frontend locale JSON for the given language.
func GetFrontendLocale(lang string) (json.RawMessage, bool) {
	ld, ok := locales[lang]
	if !ok {
		return nil, false
	}
	return ld.frontend, len(ld.frontend) > 0
}

// SupportedLanguages returns a sorted list of available locale codes.
func SupportedLanguages() []string {
	result := make([]string, len(supportedLanguages))
	copy(result, supportedLanguages)
	return result
}

// NegotiateLanguage parses an Accept-Language header value and returns the best
// matching supported language code. Defaults to "en" if no match is found.
func NegotiateLanguage(acceptLanguage string) string {
	if acceptLanguage == "" {
		return defaultLanguage()
	}

	tags, _, err := language.ParseAcceptLanguage(acceptLanguage)
	if err != nil || len(tags) == 0 {
		return defaultLanguage()
	}

	_, idx, conf := matcher.Match(tags...)
	if conf == language.No {
		return defaultLanguage()
	}

	if idx < len(supportedLanguages) {
		return supportedLanguages[idx]
	}

	return defaultLanguage()
}

// defaultLanguage returns "en" if supported, otherwise the first available language.
func defaultLanguage() string {
	if _, ok := locales["en"]; ok {
		return "en"
	}
	if len(supportedLanguages) > 0 {
		return supportedLanguages[0]
	}
	return "en"
}
