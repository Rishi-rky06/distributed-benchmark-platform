package utils

import (
	"fmt"
	"mime/multipart"
	"path/filepath"
	"strings"
)

// ── Submission upload validation ──────────────────────────────────────────────

// AllowedLanguages is the set of supported submission languages.
var AllowedLanguages = map[string]bool{
	"go":     true,
	"cpp":    true,
	"rust":   true,
	"python": true,
}

// AllowedExtensions maps language → accepted file extensions.
var AllowedExtensions = map[string][]string{
	"go":     {".go", ".tar.gz", ".zip"},
	"cpp":    {".cpp", ".cc", ".tar.gz", ".zip"},
	"rust":   {".rs", ".tar.gz", ".zip"},
	"python": {".py", ".tar.gz", ".zip"},
}

// ValidateSubmissionFile checks that an uploaded file is within size limits
// and has an extension consistent with the declared language.
func ValidateSubmissionFile(fh *multipart.FileHeader, language string, maxMB int64) error {
	// Size check
	maxBytes := maxMB * 1024 * 1024
	if fh.Size > maxBytes {
		return fmt.Errorf("file too large: %d bytes (max %d MB)", fh.Size, maxMB)
	}

	// Language check
	lang := strings.ToLower(language)
	if !AllowedLanguages[lang] {
		return fmt.Errorf("unsupported language %q; allowed: go, cpp, rust, python", language)
	}

	// Extension check
	ext := strings.ToLower(filepath.Ext(fh.Filename))
	allowed := AllowedExtensions[lang]
	for _, a := range allowed {
		if ext == a {
			return nil
		}
	}
	return fmt.Errorf("extension %q not allowed for language %q; expected one of %v", ext, lang, allowed)
}

// ── String helpers ────────────────────────────────────────────────────────────

// Truncate caps a string at n runes, appending "…" if trimmed.
func Truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// NormalizeLanguage lowercases and validates a language string.
func NormalizeLanguage(lang string) (string, error) {
	l := strings.ToLower(strings.TrimSpace(lang))
	if !AllowedLanguages[l] {
		return "", fmt.Errorf("unsupported language %q; allowed: go, cpp, rust, python", lang)
	}
	return l, nil
}

// ── Pagination ────────────────────────────────────────────────────────────────

const (
	DefaultPage     = 1
	DefaultPageSize = 20
	MaxPageSize     = 100
)

// Pagination holds validated page/size values.
type Pagination struct {
	Page   int
	Size   int
	Offset int
}

// ParsePagination clamps page and size into safe ranges.
func ParsePagination(page, size int) Pagination {
	if page < 1 {
		page = DefaultPage
	}
	if size < 1 || size > MaxPageSize {
		size = DefaultPageSize
	}
	return Pagination{
		Page:   page,
		Size:   size,
		Offset: (page - 1) * size,
	}
}

// TotalPages computes the number of pages for a given total count.
func TotalPages(total int64, size int) int {
	if size <= 0 {
		return 0
	}
	pages := int(total) / size
	if int(total)%size != 0 {
		pages++
	}
	return pages
}
