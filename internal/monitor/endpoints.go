package monitor

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/impiperl/Tweedle/internal/config"
)

type EndpointStatus struct {
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Status    int       `json:"status,omitempty"`
	LatencyMs int64     `json:"latencyMs"`
	Error     string    `json:"error,omitempty"`
	Checked   time.Time `json:"checked"`
	Level     Level     `json:"level"`
}

// endpointChecker requests every configured URL once per tick, in parallel.
// Each request uses a new connection, so a broken TLS setup or DNS shows up
// right away instead of hiding behind a kept-alive connection.
type endpointChecker struct {
	targets []config.Endpoint
	strict  *http.Client
	lax     *http.Client
}

// newEndpointChecker keeps only the enabled targets.
func newEndpointChecker(all []config.Endpoint) *endpointChecker {
	var targets []config.Endpoint
	for _, t := range all {
		if t.Enabled {
			targets = append(targets, t)
		}
	}
	client := func(verify bool) *http.Client {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.DisableKeepAlives = true
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: !verify}
		return &http.Client{Transport: tr}
	}
	return &endpointChecker{targets: targets, strict: client(true), lax: client(false)}
}

func (c *endpointChecker) check() ([]EndpointStatus, []Sample) {
	status := make([]EndpointStatus, len(c.targets))
	var wg sync.WaitGroup
	for i, t := range c.targets {
		wg.Add(1)
		go func(i int, t config.Endpoint) {
			defer wg.Done()
			status[i] = c.request(t)
		}(i, t)
	}
	wg.Wait()
	samples := make([]Sample, len(c.targets))
	for i, t := range c.targets {
		samples[i] = endpointSample(t, status[i])
	}
	return status, samples
}

func (c *endpointChecker) request(t config.Endpoint) EndpointStatus {
	st := EndpointStatus{Name: t.Name, URL: t.URL, Checked: time.Now()}
	ctx, cancel := context.WithTimeout(context.Background(), t.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.URL, nil)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	req.Header.Set("User-Agent", "tweedle")
	client := c.strict
	if !t.ShouldVerify() {
		client = c.lax
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err == nil {
		_, _ = io.CopyN(io.Discard, resp.Body, 64<<10)
		resp.Body.Close()
	}
	st.LatencyMs = time.Since(start).Milliseconds()
	switch {
	case err != nil:
		st.Error = shortError(err)
	case t.Status != 0 && resp.StatusCode != t.Status:
		st.Status = resp.StatusCode
		st.Error = fmt.Sprintf("HTTP %d, expected %d", resp.StatusCode, t.Status)
	case t.Status == 0 && resp.StatusCode >= 400:
		st.Status = resp.StatusCode
		st.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
	default:
		st.Status = resp.StatusCode
	}
	return st
}

// shortError drops the 'Get "https://…":' prefix and names timeouts plainly.
func shortError(err error) string {
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return "timeout"
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}

func endpointSample(t config.Endpoint, st EndpointStatus) Sample {
	p := map[string]string{"what": t.Name}
	s := Sample{Key: "endpoint:" + t.Name, Level: LevelOK, Label: "lbl_endpoint", Params: p}
	switch {
	case st.Error != "":
		s.Level, s.Code = LevelError, "endpoint_down"
		p["error"] = st.Error
	case t.Slow > 0 && st.LatencyMs > t.Slow.Milliseconds():
		s.Level, s.Code = LevelWarn, "endpoint_slow"
		p["value"] = strconv.FormatInt(st.LatencyMs, 10)
		p["limit"] = strconv.FormatInt(t.Slow.Milliseconds(), 10)
	}
	return s
}
