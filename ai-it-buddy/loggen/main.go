// Command loggen - имитация access-логов NGINX корпоративных сервисов (VPN, GitLab) для incident-radar.
// Пишет строки в формате combined в <LOG_DIR>/<service>.log. Сбой включается ручкой POST /outages:
// заданная доля запросов к сервису начинает отвечать 5xx, и радар это видит.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// endpoint - путь сервиса и его доля в трафике
type endpoint struct {
	method, path string
	weight       int
	size         int  // примерный размер ответа
	core         bool // основной путь: при сбое падает он
}

// profiles - трафик сервисов, похожий на настоящий
var profiles = map[string][]endpoint{
	"vpn": {
		{"POST", "/api/v1/connect", 5, 512, true},
		{"GET", "/api/v1/session", 8, 256, true},
		{"POST", "/api/v1/keepalive", 12, 64, true},
		{"GET", "/healthz", 2, 3, false},
	},
	"gitlab": {
		{"GET", "/api/v4/projects", 6, 18000, true},
		{"GET", "/payments/payments-api/-/merge_requests", 4, 42000, true},
		{"POST", "/api/v4/jobs/request", 8, 900, true},
		{"GET", "/users/sign_in", 2, 9000, false},
		{"GET", "/-/readiness", 2, 40, false},
	},
}

var agents = []string{
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) Chrome/131.0",
	"Mozilla/5.0 (X11; Linux x86_64) Firefox/133.0",
	"git/2.47.0", "gitlab-runner 17.6.0", "openvpn-connect/3.5", "curl/8.10.1",
}

// generator - пишет логи и хранит текущие сбои
type generator struct {
	dir      string
	services []string
	maxBytes int64
	rnd      *rand.Rand

	mu      sync.Mutex
	outages map[string]float64 // сервис -> доля 5xx
	files   map[string]*os.File
}

func newGenerator(dir string, services []string, maxBytes int64, seed uint64) (*generator, error) {
	for _, s := range services {
		if _, ok := profiles[s]; !ok {
			return nil, fmt.Errorf("unknown service %q (known: vpn, gitlab)", s)
		}
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("log dir: %w", err)
	}
	return &generator{
		dir: dir, services: services, maxBytes: maxBytes, rnd: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)), //nolint:gosec // синтетический трафик, не криптография
		outages: map[string]float64{}, files: map[string]*os.File{},
	}, nil
}

// line - одна строка лога; при сбое основной путь отвечает 502/503/504
func (g *generator) line(service string, at time.Time) string {
	eps := profiles[service]
	total := 0
	for _, e := range eps {
		total += e.weight
	}
	pick := g.rnd.IntN(total)
	ep := eps[0]
	for _, e := range eps {
		if pick < e.weight {
			ep = e
			break
		}
		pick -= e.weight
	}
	status := 200
	switch {
	case ep.core && g.rnd.Float64() < g.outages[service]:
		status = []int{502, 503, 504}[g.rnd.IntN(3)]
	case g.rnd.Float64() < 0.02:
		status = []int{401, 403, 404}[g.rnd.IntN(3)]
	case ep.method == "POST" && g.rnd.Float64() < 0.1:
		status = 201
	}
	size := ep.size/2 + g.rnd.IntN(ep.size+1)
	if status >= 500 {
		size = 150 + g.rnd.IntN(50)
	}
	ip := fmt.Sprintf("10.20.%d.%d", g.rnd.IntN(8), 2+g.rnd.IntN(250))
	return fmt.Sprintf(`%s - - [%s] "%s %s HTTP/1.1" %d %d "-" "%s"`,
		ip, at.Format("02/Jan/2006:15:04:05 -0700"), ep.method, ep.path, status, size, agents[g.rnd.IntN(len(agents))])
}

// write - дописать строки в лог сервиса; файл больше лимита переименовывается в .1 (простая ротация)
func (g *generator) write(service string, lines []string) error {
	f := g.files[service]
	path := filepath.Join(g.dir, service+".log")
	if f != nil {
		if st, err := f.Stat(); err == nil && st.Size() > g.maxBytes {
			_ = f.Close()
			if err := os.Rename(path, path+".1"); err != nil {
				return fmt.Errorf("rotate %s: %w", path, err)
			}
			f = nil
		}
	}
	if f == nil {
		var err error
		f, err = os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644) //nolint:gosec // путь из списка известных сервисов
		if err != nil {
			return fmt.Errorf("open %s: %w", path, err)
		}
		g.files[service] = f
	}
	_, err := io.WriteString(f, strings.Join(lines, "\n")+"\n")
	return err
}

// tick - очередная порция трафика: в среднем rps запросов в секунду на сервис
func (g *generator) tick(now time.Time, rps int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, s := range g.services {
		n := rps/2 + g.rnd.IntN(rps+1)
		lines := make([]string, 0, n)
		for range n {
			lines = append(lines, g.line(s, now.Add(-time.Duration(g.rnd.IntN(1000))*time.Millisecond)))
		}
		if err := g.write(s, lines); err != nil {
			return err
		}
	}
	return nil
}

func (g *generator) close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, f := range g.files {
		_ = f.Close()
	}
}

// outage - ручка демо: включить или выключить сбой
type outage struct {
	Service   string  `json:"service"`
	ErrorRate float64 `json:"error_rate"` // 0 - сбоя нет, 0.7 - 70% запросов к основным путям отвечают 5xx
}

func (g *generator) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok\n") })
	mux.HandleFunc("GET /outages", func(w http.ResponseWriter, _ *http.Request) { g.writeOutages(w) })
	mux.HandleFunc("POST /outages", func(w http.ResponseWriter, r *http.Request) {
		var in outage
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in); err != nil {
			http.Error(w, "body must be JSON {service, error_rate}", http.StatusBadRequest)
			return
		}
		if !slices.Contains(g.services, in.Service) {
			http.Error(w, "unknown service: "+in.Service, http.StatusBadRequest)
			return
		}
		if in.ErrorRate < 0 || in.ErrorRate > 1 {
			http.Error(w, "error_rate must be within 0..1", http.StatusBadRequest)
			return
		}
		g.mu.Lock()
		if in.ErrorRate == 0 {
			delete(g.outages, in.Service)
		} else {
			g.outages[in.Service] = in.ErrorRate
		}
		g.mu.Unlock()
		slog.Info("outage changed", slog.String("service", in.Service), slog.Float64("error_rate", in.ErrorRate))
		g.writeOutages(w)
	})
	return mux
}

func (g *generator) writeOutages(w http.ResponseWriter) {
	g.mu.Lock()
	out := map[string]float64{}
	for _, s := range g.services {
		out[s] = g.outages[s]
	}
	g.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"outages": out})
}

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck(env("LOGGEN_ADDR", ":8092")))
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	if err := run(); err != nil {
		slog.Error("loggen stopped", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func run() error {
	rps, err := strconv.Atoi(env("LOGGEN_RPS", "6"))
	if err != nil || rps < 1 {
		return errors.New("LOGGEN_RPS must be a positive integer")
	}
	maxMB, err := strconv.Atoi(env("LOGGEN_MAX_MB", "20"))
	if err != nil || maxMB < 1 {
		return errors.New("LOGGEN_MAX_MB must be a positive integer")
	}
	g, err := newGenerator(env("LOG_DIR", "/logs"), strings.Split(env("LOGGEN_SERVICES", "vpn,gitlab"), ","), int64(maxMB)<<20, uint64(time.Now().UnixNano()))
	if err != nil {
		return err
	}
	defer g.close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv := &http.Server{Addr: env("LOGGEN_ADDR", ":8092"), Handler: g.handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http", slog.String("error", err.Error()))
			stop()
		}
	}()
	slog.Info("loggen started", slog.String("dir", g.dir), slog.Any("services", g.services), slog.Int("rps", rps))

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			return srv.Shutdown(shutdown)
		case now := <-ticker.C:
			if err := g.tick(now, rps); err != nil {
				return err
			}
		}
	}
}

// healthcheck - для HEALTHCHECK в distroless-образе
func healthcheck(addr string) int {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+port+"/healthz", http.NoBody)
	if err != nil {
		return 1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 1
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
