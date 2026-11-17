package monitor

import (
	"errors"
	"testing"
	"time"

	"github.com/impiperl/Tweedle/internal/collect"
	"github.com/impiperl/Tweedle/internal/config"
)

func testCerts(notAfter time.Time, err error) (*certChecker, *clock, *int) {
	c := &clock{t: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}
	calls := 0
	cc := newCertChecker(config.Certs{CheckEvery: time.Hour, WarnDays: 14, ErrorDays: 3,
		Targets: []config.CertTarget{{Name: "x", Host: "x:443"}}})
	cc.now = c.now
	cc.read = func(config.CertTarget) (*collect.CertInfo, error) {
		calls++
		if err != nil {
			return nil, err
		}
		return &collect.CertInfo{NotAfter: notAfter}, nil
	}
	return cc, c, &calls
}

func TestCertLevels(t *testing.T) {
	base := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		days  float64
		level Level
		code  string
	}{
		{30, LevelOK, ""},
		{13.5, LevelWarn, "cert_expiring"},
		{2.5, LevelError, "cert_expiring"},
		{-1, LevelError, "cert_expired"},
	}
	for _, tc := range cases {
		cc, _, _ := testCerts(base.Add(time.Duration(tc.days*24)*time.Hour), nil)
		_, samples := cc.check()
		if samples[0].Level != tc.level || samples[0].Code != tc.code {
			t.Errorf("%v days: got %s/%s, want %s/%s", tc.days, samples[0].Level, samples[0].Code, tc.level, tc.code)
		}
	}
}

func TestCertSuccessIsCachedFailureRetried(t *testing.T) {
	cc, c, calls := testCerts(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), nil)
	cc.check()
	c.add(30 * time.Minute)
	cc.check()
	if *calls != 1 {
		t.Fatalf("success should be cached for CheckEvery, calls=%d", *calls)
	}
	c.add(31 * time.Minute)
	cc.check()
	if *calls != 2 {
		t.Fatalf("expected new check after CheckEvery, calls=%d", *calls)
	}

	cf, _, fcalls := testCerts(time.Time{}, errors.New("refused"))
	_, s := cf.check()
	cf.check()
	if *fcalls != 2 || s[0].Code != "cert_failed" {
		t.Fatalf("failure should be retried every tick: calls=%d code=%s", *fcalls, s[0].Code)
	}
}

// Many targets checked in parallel, some cached and some due: run with -race.
func TestCertParallelChecksNoRace(t *testing.T) {
	var targets []config.CertTarget
	for i := 0; i < 20; i++ {
		targets = append(targets, config.CertTarget{Name: string(rune('a' + i)), Host: "x:443"})
	}
	c := &clock{t: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}
	cc := newCertChecker(config.Certs{CheckEvery: time.Hour, WarnDays: 14, ErrorDays: 3, Targets: targets})
	cc.now = c.now
	cc.read = func(t config.CertTarget) (*collect.CertInfo, error) {
		time.Sleep(time.Millisecond)
		if t.Name[0]%2 == 0 {
			return nil, errors.New("refused") // retried on every tick
		}
		return &collect.CertInfo{NotAfter: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)}, nil
	}
	for i := 0; i < 5; i++ {
		if st, _ := cc.check(); len(st) != len(targets) {
			t.Fatalf("got %d results", len(st))
		}
		c.add(time.Minute)
	}
}
