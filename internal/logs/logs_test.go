package logs

import (
	"strings"
	"testing"
	"time"

	"github.com/impiperl/Tweedle/internal/collect"
)

func TestRedact(t *testing.T) {
	r := NewRedactor([]string{"session"})
	cases := map[string]string{
		"login ok for anna.maier+test@example.co.uk from 1.2.3.4": "login ok for ***@*** from 1.2.3.4",
		"password=hunter2 user=bob":                               "password=*** user=bob",
		`{"user":"x","password":"a b c","n":1}`:                   `{"user":"x","password":***,"n":1}`,
		"spring.datasource.password: geheim":                      "spring.datasource.password: ***",
		"Authorization: Bearer abc.def.ghi":                       "Authorization: ***",
		"X-API-KEY=Zk39dk3kd0":                                    "X-API-KEY=***",
		"GET /x?access_token=abc123&page=2":                       "GET /x?access_token=***&page=2",
		"connect postgres://kupiter:s3cr3t@personaX:5432/db":      "connect postgres://kupiter:***@personaX:5432/db",
		"jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.sig-part_1 end": "jwt *** end",
		"sent header bearer abcdefghijklmnop":                     "sent header bearer ***",
		"session_id=77abc; path=/":                                "session_id=***; path=/",
		"password reset requested for user 5":                     "password reset requested for user 5",
	}
	for in, want := range cases {
		if got := r.Redact(in); got != want {
			t.Errorf("Redact(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

func TestLineLevel(t *testing.T) {
	cases := map[string]string{
		"2026-11-19 10:00:00.123 ERROR 1 --- [main] c.g.Foo : boom": "error",
		`2026/11/19 10:00:00 [error] 29#29: *1 connect() failed`:    "error",
		"ERROR:  duplicate key value violates unique constraint":    "error",
		"FATAL:  password authentication failed":                    "error",
		"java.lang.NullPointerException: null":                      "error",
		"Exception in thread \"main\" java.lang.OutOfMemoryError":   "error",
		"panic: runtime error: index out of range":                  "error",
		"2026-11-19 10:00:00.123  WARN 1 --- [main] c.g.Foo : slow": "warn",
		"WARNING: something": "warn",
		"2026-11-19 10:00:00.123  INFO 1 --- [main] c.g.Foo : started": "info",
		"0 errors, 2 tests": "info",
		"at com.gaister.ErrorHandler.handle(ErrorHandler.java:12)": "info",
	}
	for in, want := range cases {
		if got := LineLevel(in); got != want {
			t.Errorf("LineLevel(%q) = %s, want %s", in, got, want)
		}
	}
}

func lines(texts ...string) []collect.LogLine {
	out := make([]collect.LogLine, len(texts))
	base := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	for i, s := range texts {
		out[i] = collect.LogLine{Time: base.Add(time.Duration(i) * time.Second), Text: s}
	}
	return out
}

func TestGroupKeepsStackTraceTogether(t *testing.T) {
	es := Group(lines(
		"INFO started",
		"ERROR request failed for a@b.de",
		"java.lang.IllegalStateException: bad",
		"\tat com.x.Foo.bar(Foo.java:1)",
		"\tat com.x.Foo.baz(Foo.java:2)",
		"Caused by: java.io.IOException: closed",
		"\t... 12 more",
		"INFO next",
	), NewRedactor(nil))
	if len(es) != 4 {
		t.Fatalf("got %d entries: %+v", len(es), es)
	}
	if es[1].Level != "error" || es[1].Text != "ERROR request failed for ***@***" {
		t.Errorf("entry 1: %+v", es[1])
	}
	// The exception line itself is not indented, so it starts a new entry
	// that collects the frames and the cause.
	if got := strings.Count(es[2].Text, "\n"); got != 4 || es[2].Level != "error" {
		t.Errorf("entry 2 (%d newlines): %+v", got, es[2])
	}
	if es[3].Text != "INFO next" || es[3].Level != "info" {
		t.Errorf("entry 3: %+v", es[3])
	}
	if es[0].Time == nil || !es[0].Time.Equal(time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("time: %v", es[0].Time)
	}
}

func TestGroupCapsLongTraces(t *testing.T) {
	in := []string{"ERROR boom"}
	for i := 0; i < maxEntryLines+50; i++ {
		in = append(in, "\tat frame")
	}
	es := Group(lines(in...), NewRedactor(nil))
	if len(es) != 1 {
		t.Fatalf("got %d entries", len(es))
	}
	if n := strings.Count(es[0].Text, "\n") + 1; n != maxEntryLines+1 || !strings.HasSuffix(es[0].Text, "\n…") {
		t.Errorf("%d lines, suffix %q", n, es[0].Text[len(es[0].Text)-5:])
	}
}

func TestFilter(t *testing.T) {
	es := []Entry{
		{Level: "info", Text: "a"}, {Level: "warn", Text: "slow DB"}, {Level: "error", Text: "db down"},
		{Level: "info", Text: "db ok"}, {Level: "error", Text: "disk"},
	}
	if got, _ := Filter(es, "all", "", 10); len(got) != 5 {
		t.Errorf("all: %d", len(got))
	}
	if got, _ := Filter(es, "warn", "", 10); len(got) != 3 {
		t.Errorf("warn: %d", len(got))
	}
	if got, _ := Filter(es, "error", "DB", 10); len(got) != 1 || got[0].Text != "db down" {
		t.Errorf("error+db: %+v", got)
	}
	got, tr := Filter(es, "all", "", 2)
	if !tr || len(got) != 2 || got[1].Text != "disk" {
		t.Errorf("limit: %v %+v", tr, got)
	}
}
