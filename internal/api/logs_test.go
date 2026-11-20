package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/impiperl/Tweedle/internal/collect"
	"github.com/impiperl/Tweedle/internal/config"
)

func getJSON(t *testing.T, ts *httptest.Server, tok, path string, v any) int {
	t.Helper()
	req, _ := http.NewRequest("GET", ts.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if v != nil {
		_ = json.NewDecoder(resp.Body).Decode(v)
	}
	return resp.StatusCode
}

func TestContainerLogs(t *testing.T) {
	srv, st, ts := newTestServer(t)
	tok := pair(t, st, ts, "de")
	srv.cfg.Containers = []config.Container{{Name: "app1"}}
	var gotName string
	var gotSince time.Time
	var gotTail int
	srv.fetchLogs = func(cmd []string, name string, since time.Time, tail int) ([]collect.LogLine, error) {
		gotName, gotSince, gotTail = name, since, tail
		now := time.Now()
		return []collect.LogLine{
			{Time: now, Text: "INFO started"},
			{Time: now, Text: "ERROR mail to x@y.com failed, password=abc"},
			{Time: now, Text: "\tat com.x.Foo(Foo.java:1)"},
			{Time: now, Text: "WARN slow"},
		}, nil
	}

	var out struct {
		Container string
		Entries   []struct{ Level, Text string }
		Truncated bool
	}
	if code := getJSON(t, ts, tok, "/api/containers/app1/logs?level=error", &out); code != 200 {
		t.Fatalf("status %d", code)
	}
	if gotName != "app1" || gotTail != logScan || time.Since(gotSince) < 59*time.Minute || time.Since(gotSince) > 61*time.Minute {
		t.Errorf("fetch called with %q %v %d", gotName, gotSince, gotTail)
	}
	if len(out.Entries) != 1 || out.Entries[0].Text != "ERROR mail to ***@*** failed, password=***\n\tat com.x.Foo(Foo.java:1)" {
		t.Errorf("entries: %+v", out.Entries)
	}

	if code := getJSON(t, ts, tok, "/api/containers/app1/logs?since=all&q=slow", &out); code != 200 || len(out.Entries) != 1 || !gotSince.IsZero() {
		t.Errorf("search: %d %+v since=%v", code, out.Entries, gotSince)
	}
	if code := getJSON(t, ts, tok, "/api/containers/other/logs", nil); code != 404 {
		t.Errorf("unwatched container: %d", code)
	}
	if code := getJSON(t, ts, tok, "/api/containers/app1/logs?since=2y", nil); code != 400 {
		t.Errorf("bad since: %d", code)
	}
	if code := getJSON(t, ts, "wrong", "/api/containers/app1/logs", nil); code != 401 {
		t.Errorf("bad token: %d", code)
	}

	srv.fetchLogs = func([]string, string, time.Time, int) ([]collect.LogLine, error) {
		return nil, errors.New("no such container")
	}
	var e struct{ Error string }
	if code := getJSON(t, ts, tok, "/api/containers/app1/logs", &e); code != 502 || e.Error != "no such container" {
		t.Errorf("podman error: %d %q", code, e.Error)
	}

	var status struct{ Logs bool }
	getJSON(t, ts, tok, "/api/status", &status)
	if !status.Logs {
		t.Error("status.logs should be true")
	}
	srv.cfg.Logs.Enabled = false
	if code := getJSON(t, ts, tok, "/api/containers/app1/logs", nil); code != 403 {
		t.Errorf("disabled: %d", code)
	}
	getJSON(t, ts, tok, "/api/status", &status)
	if status.Logs {
		t.Error("status.logs should be false")
	}
}
