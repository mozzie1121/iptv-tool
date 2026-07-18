package i18n

import (
	"github.com/gin-gonic/gin"
)

const langContextKey = "lang"

// Middleware returns a Gin middleware that resolves the request language.
// It checks the X-Language header first (frontend manual override), then
// falls back to the Accept-Language header. The resolved language is stored
// in the Gin context as "lang".
func Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		lang := c.GetHeader("X-Language")

		if lang == "" {
			lang = NegotiateLanguage(c.GetHeader("Accept-Language"))
		} else {
			// Validate the explicitly requested language is supported
			supported := false
			for _, s := range SupportedLanguages() {
				if s == lang {
					supported = true
					break
				}
			}
			if !supported {
				lang = NegotiateLanguage(lang)
			}
		}

		c.Set(langContextKey, lang)
		c.Next()
	}
}

// Lang retrieves the resolved language from the Gin context.
// Returns "en" if the language was not set.
func Lang(c *gin.Context) string {
	if lang, exists := c.Get(langContextKey); exists {
		if s, ok := lang.(string); ok {
			return s
		}
	}
	return "en"
}
