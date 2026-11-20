// Package logs prepares container log lines for the app: secrets and mail
// addresses are hidden, multi-line entries (stack traces) are kept together,
// each entry gets a level, and entries can be filtered by level and text.
package logs

import (
	"regexp"
	"strings"
	"time"

	"github.com/impiperl/Tweedle/internal/collect"
)

// Entry is one log message, possibly several lines long (e.g. an exception
// with its stack trace).
type Entry struct {
	Time  *time.Time `json:"time,omitempty"`
	Level string     `json:"level"` // error, warn or info
	Text  string     `json:"text"`
}

// Limits of one entry, so a runaway trace does not fill the answer.
const (
	maxEntryLines = 200
	maxEntryBytes = 32 << 10
)

// DefaultRedactKeys are always hidden; the config can add more.
var DefaultRedactKeys = []string{
	"password", "passwd", "pwd", "secret", "token", "apikey", "api_key", "api-key",
	"authorization", "credential", "private_key", "cookie",
}

// Redactor hides secrets in log text.
type Redactor struct {
	keyValue *regexp.Regexp
}

var (
	mailRE    = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,}`)
	urlAuthRE = regexp.MustCompile(`(://[^/\s:@]+:)[^/\s@]+@`)
	jwtRE     = regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{5,}\.[A-Za-z0-9_\-]{5,}\.[A-Za-z0-9_\-]+`)
	bearerRE  = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=\-]{8,}`)
)

// NewRedactor builds a redactor for the default keys plus extra.
func NewRedactor(extra []string) *Redactor {
	keys := make([]string, 0, len(DefaultRedactKeys)+len(extra))
	for _, k := range append(append([]string{}, DefaultRedactKeys...), extra...) {
		if k = strings.TrimSpace(k); k != "" {
			keys = append(keys, regexp.QuoteMeta(k))
		}
	}
	// A key (any word containing one of the names, optionally quoted), then
	// = or :, then the value: quoted, "Bearer xyz" or up to the next separator.
	kv := `(?i)(["']?[\w.\-]*(?:` + strings.Join(keys, "|") + `)[\w.\-]*["']?\s*[:=]\s*)` +
		`(?:"[^"]*"|'[^']*'|(?:bearer|basic)\s+\S+|[^\s"',;&}\]]+)`
	return &Redactor{keyValue: regexp.MustCompile(kv)}
}

// Redact hides mail addresses, passwords in URLs, JWTs, bearer tokens and
// the values of secret keys.
func (r *Redactor) Redact(s string) string {
	s = urlAuthRE.ReplaceAllString(s, "${1}***@")
	s = r.keyValue.ReplaceAllString(s, "${1}***")
	s = bearerRE.ReplaceAllString(s, "${1} ***")
	s = jwtRE.ReplaceAllString(s, "***")
	return mailRE.ReplaceAllString(s, "***@***")
}

var (
	// Levels as written by Java/Spring, nginx, PostgreSQL, Go, Python and
	// most other loggers; class names like FooException or OutOfMemoryError.
	errorRE = regexp.MustCompile(`(?i:\b(?:error|fatal|severe|critical|crit|emerg|alert|panic|traceback)\b)|[a-z](?:Exception|Error)\b`)
	warnRE  = regexp.MustCompile(`(?i:\b(?:warn|warning)\b)`)
	// Lines that continue the previous entry: indented, Java stack frames,
	// "Caused by:", "... 12 more".
	contRE = regexp.MustCompile(`^(?:\s|at\s|Caused by:|Suppressed:|\.\.\. \d+ (?:more|common frames omitted))`)
)

// LineLevel classifies a single line.
func LineLevel(s string) string {
	switch {
	case errorRE.MatchString(s):
		return "error"
	case warnRE.MatchString(s):
		return "warn"
	}
	return "info"
}

var rank = map[string]int{"info": 0, "warn": 1, "error": 2}

// Group redacts the lines and joins continuation lines to the entry before.
// The level of an entry is the worst level of its lines.
func Group(lines []collect.LogLine, r *Redactor) []Entry {
	var out []Entry
	var b strings.Builder
	n, full := 0, false
	flush := func() {
		if len(out) > 0 {
			out[len(out)-1].Text = b.String()
		}
		b.Reset()
	}
	for _, l := range lines {
		text := strings.TrimRight(r.Redact(l.Text), "\r")
		lvl := LineLevel(text)
		if len(out) > 0 && strings.TrimSpace(text) != "" && contRE.MatchString(text) {
			e := &out[len(out)-1]
			if rank[lvl] > rank[e.Level] {
				e.Level = lvl
			}
			switch {
			case full:
			case n >= maxEntryLines || b.Len() >= maxEntryBytes:
				b.WriteString("\n…")
				full = true
			default:
				b.WriteByte('\n')
				b.WriteString(text)
				n++
			}
			continue
		}
		flush()
		e := Entry{Level: lvl}
		if !l.Time.IsZero() {
			t := l.Time
			e.Time = &t
		}
		out = append(out, e)
		b.WriteString(text)
		n, full = 1, false
	}
	flush()
	return out
}

// Filter keeps the entries at or above level ("all", "warn", "error") that
// contain q (case-insensitive, empty = any), and of those the last max.
// truncated reports whether more matched.
func Filter(entries []Entry, level, q string, max int) (out []Entry, truncated bool) {
	min := 0
	switch level {
	case "warn":
		min = 1
	case "error":
		min = 2
	}
	q = strings.ToLower(strings.TrimSpace(q))
	for _, e := range entries {
		if rank[e.Level] < min {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(e.Text), q) {
			continue
		}
		out = append(out, e)
	}
	if len(out) > max {
		return out[len(out)-max:], true
	}
	return out, false
}
