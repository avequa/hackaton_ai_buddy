package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/avequa/ai-it-buddy/core/internal/domain"
	"github.com/avequa/ai-it-buddy/core/internal/httpapi"
	"github.com/avequa/ai-it-buddy/core/internal/metrics"
	"github.com/avequa/ai-it-buddy/core/internal/store"
)

// persister - фоновая запись изменений ядра в Postgres. Ядро отвечает из памяти, раз в секунду
// изменения уходят в базу одной транзакцией. База недоступна - изменения копятся и уйдут, когда она вернется.
type persister struct {
	core    *domain.Core
	store   *store.Store
	metrics *metrics.Metrics
	logger  *slog.Logger

	mu      sync.Mutex
	lastOK  time.Time
	lastErr error
	failing bool
}

// openStorage - подключение, восстановление состояния и синхронизация базы знаний.
// На пустой базе записывается начальное состояние из YAML.
func openStorage(ctx context.Context, url string, wait time.Duration, core *domain.Core, m *metrics.Metrics, logger *slog.Logger) (*persister, error) {
	openCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	st, err := store.Open(openCtx, url)
	if err != nil {
		return nil, err
	}
	state, initialized, err := st.Load(ctx)
	if err != nil {
		st.Close()
		return nil, fmt.Errorf("load state: %w", err)
	}
	if initialized {
		core.Restore(state)
	} else {
		core.MarkAllDirty()
	}
	if err := st.SyncDocs(ctx, core.Docs()); err != nil {
		st.Close()
		return nil, fmt.Errorf("sync knowledge base: %w", err)
	}
	core.SetKB(st)
	p := &persister{core: core, store: st, metrics: m, logger: logger}
	if err := p.flush(ctx); err != nil {
		st.Close()
		return nil, fmt.Errorf("initial write: %w", err)
	}
	logger.Info("state restored from postgres", slog.Bool("fresh_db", !initialized),
		slog.Int("requests", len(state.Requests)), slog.Int("notifications", len(state.Notes)))
	return p, nil
}

func (p *persister) flush(ctx context.Context) error {
	changes, commit := p.core.TakeChanges()
	var err error
	if !changes.Empty() {
		err = p.store.Save(ctx, changes)
	}
	commit(err == nil)
	p.metrics.StorePending.Set(float64(p.core.PendingChanges()))

	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil {
		p.metrics.StoreFlushes.WithLabelValues("error").Inc()
		if !p.failing {
			p.logger.Error("postgres write failed, keeping changes in memory", slog.String("error", err.Error()), slog.Int("changes", changes.Size()))
		}
		p.failing, p.lastErr = true, err
		return err
	}
	if !changes.Empty() {
		p.metrics.StoreFlushes.WithLabelValues("ok").Inc()
	}
	if p.failing {
		p.logger.Info("postgres is back, pending changes written", slog.Int("changes", changes.Size()))
	}
	p.failing, p.lastErr, p.lastOK = false, nil, time.Now()
	p.metrics.StoreLastSuccess.Set(float64(p.lastOK.Unix()))
	return nil
}

// loop - запись раз в interval до отмены; при остановке - последняя запись с отдельным таймаутом
func (p *persister) loop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			final, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			if err := p.flush(final); err != nil {
				p.logger.Error("final write failed, unsaved changes lost", slog.Int("changes", p.core.PendingChanges()))
			}
			cancel()
			return
		case <-ticker.C:
			opCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_ = p.flush(opCtx)
			cancel()
		}
	}
}

func (p *persister) ready(ctx context.Context) httpapi.Readiness {
	r := httpapi.Readiness{Status: "ok", Storage: "postgres", Pending: p.core.PendingChanges()}
	p.mu.Lock()
	if !p.lastOK.IsZero() {
		t := p.lastOK.UTC()
		r.LastFlushAt = &t
	}
	if p.lastErr != nil {
		r.Status, r.Error = "degraded", p.lastErr.Error()
	}
	p.mu.Unlock()
	if err := p.store.Ping(ctx); err != nil {
		r.Status, r.Error = "degraded", err.Error()
	}
	return r
}
