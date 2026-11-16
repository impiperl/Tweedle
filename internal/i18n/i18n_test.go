package i18n

import "testing"

func TestLabelIsRenderedFirst(t *testing.T) {
	got := T("de", "recovered", map[string]string{"label": "lbl_disk", "mount": "/home"})
	if got != "Platte /home: wieder in Ordnung" {
		t.Fatalf("got %q", got)
	}
}

func TestFallbackToEnglish(t *testing.T) {
	if got := T("fr", "proc_restarted", map[string]string{"what": "nginx"}); got != "Process nginx was restarted" {
		t.Fatalf("got %q", got)
	}
}

func TestSameKeysInAllLanguages(t *testing.T) {
	for _, l := range Langs() {
		for k := range texts["en"] {
			if _, ok := texts[l][k]; !ok {
				t.Errorf("%s: missing %s", l, k)
			}
		}
		for k := range texts[l] {
			if _, ok := texts["en"][k]; !ok {
				t.Errorf("en: missing %s (present in %s)", k, l)
			}
		}
	}
}
