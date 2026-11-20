package collect

import (
	"strings"
	"testing"
)

func TestReadLogLines(t *testing.T) {
	long := strings.Repeat("x", MaxLogLineLen+100)
	in := "2026-11-19T10:00:00.123456789+01:00 hello world\n" +
		"no timestamp here\n" +
		"2026-11-19T10:00:01.5Z " + long + "\n" +
		"2026-11-19T10:00:02Z last without newline"
	got, err := readLogLines(strings.NewReader(in), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d lines", len(got))
	}
	if got[0].Text != "hello world" || got[0].Time.UTC().Hour() != 9 {
		t.Errorf("line 0: %+v", got[0])
	}
	if !got[1].Time.IsZero() || got[1].Text != "no timestamp here" {
		t.Errorf("line 1: %+v", got[1])
	}
	if !strings.HasSuffix(got[2].Text, " …") || len(got[2].Text) > MaxLogLineLen+10 {
		t.Errorf("line 2 length %d", len(got[2].Text))
	}
	if got[3].Text != "last without newline" {
		t.Errorf("line 3: %+v", got[3])
	}
}
