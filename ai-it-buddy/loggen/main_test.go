package main

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// тот же формат, что разбирает incident-radar (combined)
var combined = regexp.MustCompile(`^(\S+) - (\S+) \[([^\]]+)\] "([^"]*)" (\d{3}) (\d+) "([^"]*)" "([^"]*)"$`)

func readLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // путь из t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	return out
}

func TestGenerator(t *testing.T) {
	dir := t.TempDir()
	if _, err := newGenerator(dir, []string{"mail"}, 1<<20, 1); err == nil {
		t.Fatal("unknown service accepted")
	}
	g, err := newGenerator(dir, []string{"vpn", "gitlab"}, 1<<20, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer g.close()
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	for i := range 30 {
		if err = g.tick(now.Add(time.Duration(i)*time.Second), 10); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []string{"vpn", "gitlab"} {
		lines := readLines(t, filepath.Join(dir, s+".log"))
		if len(lines) < 100 {
			t.Fatalf("%s: %d lines", s, len(lines))
		}
		fivexx := 0
		for _, l := range lines {
			m := combined.FindStringSubmatch(l)
			if m == nil {
				t.Fatalf("bad line: %s", l)
			}
			if _, perr := time.Parse("02/Jan/2006:15:04:05 -0700", m[3]); perr != nil {
				t.Fatalf("bad time: %s", m[3])
			}
			if strings.HasPrefix(m[5], "5") {
				fivexx++
			}
		}
		if fivexx != 0 {
			t.Fatalf("%s: 5xx without outage: %d", s, fivexx)
		}
	}

	// сбой: большая доля 5xx только у vpn
	srv := httptest.NewServer(g.handler())
	defer srv.Close()
	post := func(body string) int {
		resp, perr := http.Post(srv.URL+"/outages", "application/json", strings.NewReader(body)) //nolint:noctx // тест
		if perr != nil {
			t.Fatal(perr)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	for body, code := range map[string]int{
		`{"service":"vpn","error_rate":0.8}`: 200, `{"service":"mail","error_rate":0.5}`: 400,
		`{"service":"vpn","error_rate":2}`: 400, `nope`: 400,
	} {
		if got := post(body); got != code {
			t.Fatalf("%s: %d, want %d", body, got, code)
		}
	}
	before := len(readLines(t, filepath.Join(dir, "vpn.log")))
	for i := range 20 {
		if err = g.tick(now.Add(time.Minute+time.Duration(i)*time.Second), 10); err != nil {
			t.Fatal(err)
		}
	}
	lines := readLines(t, filepath.Join(dir, "vpn.log"))[before:]
	fivexx := 0
	for _, l := range lines {
		if combined.FindStringSubmatch(l)[5][0] == '5' {
			fivexx++
		}
	}
	if rate := float64(fivexx) / float64(len(lines)); rate < 0.5 || rate > 0.95 {
		t.Fatalf("vpn 5xx rate during outage = %.2f", rate)
	}
	if post(`{"service":"vpn","error_rate":0}`) != 200 || len(g.outages) != 0 {
		t.Fatal("outage not cleared")
	}
	resp, err := http.Get(srv.URL + "/outages") //nolint:noctx // тест
	if err != nil || resp.StatusCode != 200 {
		t.Fatal("get outages")
	}
	_ = resp.Body.Close()
}

func TestRotation(t *testing.T) {
	dir := t.TempDir()
	g, err := newGenerator(dir, []string{"vpn"}, 2000, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer g.close()
	for i := range 10 {
		if err := g.tick(time.Now().Add(time.Duration(i)*time.Second), 20); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "vpn.log.1")); err != nil {
		t.Fatal("no rotated file")
	}
	if st, _ := os.Stat(filepath.Join(dir, "vpn.log")); st.Size() > 6000 {
		t.Fatalf("current log too big: %d", st.Size())
	}
}

func TestHelpers(t *testing.T) {
	t.Setenv("LOGGEN_TEST", "x")
	if env("LOGGEN_TEST", "d") != "x" || env("LOGGEN_NONE", "d") != "d" {
		t.Fatal("env")
	}
	if healthcheck("bad") == 0 || healthcheck("127.0.0.1:1") == 0 {
		t.Fatal("healthcheck without server")
	}
	t.Setenv("LOGGEN_RPS", "0")
	if run() == nil {
		t.Fatal("bad rps accepted")
	}
	t.Setenv("LOGGEN_RPS", "5")
	t.Setenv("LOGGEN_MAX_MB", "x")
	if run() == nil {
		t.Fatal("bad max accepted")
	}
}
