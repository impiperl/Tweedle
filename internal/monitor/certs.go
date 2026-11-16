package monitor

import (
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/impiperl/Tweedle/internal/collect"
	"github.com/impiperl/Tweedle/internal/config"
)

type CertStatus struct {
	Name     string    `json:"name"`
	Target   string    `json:"target"`
	NotAfter time.Time `json:"notAfter,omitempty"`
	DaysLeft int       `json:"daysLeft"`
	Issuer   string    `json:"issuer,omitempty"`
	Error    string    `json:"error,omitempty"`
	Checked  time.Time `json:"checked"`
	Level    Level     `json:"level"`
}

// certChecker checks the configured certificates at most every CheckEvery.
// Successful results are reused in between; a failed check is retried on
// the next tick, so FailAfter counts real attempts.
type certChecker struct {
	cfg  config.Certs
	now  func() time.Time
	last map[string]CertStatus
	read func(t config.CertTarget) (*collect.CertInfo, error)
}

func newCertChecker(cfg config.Certs) *certChecker {
	return &certChecker{cfg: cfg, now: time.Now, last: map[string]CertStatus{}, read: readCert}
}

func readCert(t config.CertTarget) (*collect.CertInfo, error) {
	if t.File != "" {
		return collect.CertFromFile(t.File)
	}
	return collect.CertFromHost(t.Host, t.ShouldVerify(), 10*time.Second)
}

func (c *certChecker) check() ([]CertStatus, []Sample) {
	now := c.now()
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, t := range c.cfg.Targets {
		if prev, ok := c.last[t.Name]; ok && prev.Error == "" && now.Sub(prev.Checked) < c.cfg.CheckEvery {
			continue
		}
		wg.Add(1)
		go func(t config.CertTarget) {
			defer wg.Done()
			st := CertStatus{Name: t.Name, Target: t.Host + t.File, Checked: now}
			info, err := c.read(t)
			switch {
			case err != nil:
				st.Error = err.Error()
			default:
				st.NotAfter, st.Issuer = info.NotAfter, info.Issuer
				st.DaysLeft = int(math.Floor(info.NotAfter.Sub(now).Hours() / 24))
				if info.VerifyErr != "" && now.Before(info.NotAfter) {
					st.Error = info.VerifyErr
				}
			}
			mu.Lock()
			c.last[t.Name] = st
			mu.Unlock()
		}(t)
	}
	wg.Wait()

	var status []CertStatus
	var samples []Sample
	for _, t := range c.cfg.Targets {
		st := c.last[t.Name]
		status = append(status, st)
		samples = append(samples, c.sample(st))
	}
	return status, samples
}

func (c *certChecker) sample(st CertStatus) Sample {
	p := map[string]string{"what": st.Name}
	s := Sample{Key: "cert:" + st.Name, Level: LevelOK, Label: "lbl_cert", Params: p}
	expiredOrDays := !st.NotAfter.IsZero()
	switch {
	case expiredOrDays && st.DaysLeft < 0:
		s.Level, s.Code = LevelError, "cert_expired"
		p["date"] = st.NotAfter.Format("2006-01-02")
	case st.Error != "" && !expiredOrDays:
		s.Level, s.Code = LevelError, "cert_failed"
		p["error"] = st.Error
	case st.Error != "":
		s.Level, s.Code = LevelError, "cert_invalid"
		p["error"] = st.Error
	case c.cfg.ErrorDays > 0 && st.DaysLeft < c.cfg.ErrorDays:
		s.Level, s.Code = LevelError, "cert_expiring"
	case c.cfg.WarnDays > 0 && st.DaysLeft < c.cfg.WarnDays:
		s.Level, s.Code = LevelWarn, "cert_expiring"
	}
	if s.Code == "cert_expiring" {
		p["days"] = strconv.Itoa(st.DaysLeft)
		p["date"] = st.NotAfter.Format("2006-01-02")
	}
	return s
}
