// Package store - Postgres для ядра: состояние заявок, доступов, уведомлений и поиск по базе знаний.
package store

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/avequa/ai-it-buddy/core/internal/domain"
)

//go:embed migrations/*.sql
var migrations embed.FS

// lockID - advisory lock на время миграций, чтобы два экземпляра не применили их одновременно
const lockID = 7_204_119

// Store - пул соединений с Postgres
type Store struct {
	pool *pgxpool.Pool
}

// Open - подключается и применяет миграции. Ждет базу до ctx: при старте в compose Postgres может подниматься дольше ядра.
func Open(ctx context.Context, url string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	cfg.MaxConns = 8
	// время из базы - в UTC, как в ядре; иначе после перезапуска даты придут в часовом поясе сервера
	cfg.AfterConnect = func(_ context.Context, conn *pgx.Conn) error {
		conn.TypeMap().RegisterType(&pgtype.Type{Name: "timestamptz", OID: pgtype.TimestamptzOID, Codec: &pgtype.TimestamptzCodec{ScanLocation: time.UTC}})
		return nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	for {
		err = pool.Ping(ctx)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			pool.Close()
			return nil, fmt.Errorf("postgres is not reachable: %w", err)
		case <-time.After(time.Second):
		}
	}
	s := &Store{pool: pool}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

// Close - закрыть пул
func (s *Store) Close() { s.pool.Close() }

// Ping - база доступна (для /readyz)
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *Store) migrate(ctx context.Context) error {
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	slices.Sort(files)
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, lockID); err != nil {
			return fmt.Errorf("lock migrations: %w", err)
		}
		if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version int PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
			return fmt.Errorf("create schema_migrations: %w", err)
		}
		for _, f := range files {
			name := strings.TrimPrefix(f, "migrations/")
			version, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
			if err != nil {
				return fmt.Errorf("migration %s: name must start with a number", name)
			}
			var applied bool
			if qerr := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, version).Scan(&applied); qerr != nil {
				return fmt.Errorf("check migration %d: %w", version, qerr)
			}
			if applied {
				continue
			}
			sql, err := migrations.ReadFile(f)
			if err != nil {
				return fmt.Errorf("read %s: %w", name, err)
			}
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return fmt.Errorf("apply %s: %w", name, err)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
				return fmt.Errorf("record %s: %w", name, err)
			}
		}
		return nil
	})
}

// Load - сохраненное состояние. initialized=false - база пустая, ядро должно записать начальное состояние из YAML.
func (s *Store) Load(ctx context.Context) (domain.State, bool, error) {
	var st domain.State
	var initialized bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM meta WHERE key = 'initialized')`).Scan(&initialized); err != nil {
		return st, false, fmt.Errorf("check initialized: %w", err)
	}
	if !initialized {
		return st, false, nil
	}
	loaders := []func(context.Context, *domain.State) error{
		s.loadRequests, s.loadGrants, s.loadStock, s.loadIssued, s.loadSteps,
		s.loadIncidents, s.loadNotes, s.loadSecurity, s.loadTopics, s.loadFlags,
	}
	for _, load := range loaders {
		if err := load(ctx, &st); err != nil {
			return st, true, err
		}
	}
	return st, true, nil
}

func (s *Store) loadRequests(ctx context.Context, st *domain.State) error {
	rows, err := s.pool.Query(ctx, `SELECT data, due_at FROM requests ORDER BY created_at, id`)
	if err != nil {
		return fmt.Errorf("load requests: %w", err)
	}
	st.Requests, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Request, error) {
		var data []byte
		var due *time.Time
		var r domain.Request
		if serr := row.Scan(&data, &due); serr != nil {
			return r, serr
		}
		if jerr := json.Unmarshal(data, &r); jerr != nil {
			return r, fmt.Errorf("decode request: %w", jerr)
		}
		if due != nil {
			r.DueAt = *due
		}
		return r, nil
	})
	if err != nil {
		return fmt.Errorf("load requests: %w", err)
	}
	return nil
}

func (s *Store) loadGrants(ctx context.Context, st *domain.State) error {
	rows, err := s.pool.Query(ctx, `SELECT user_id, service, project, level FROM grants ORDER BY user_id, service, project`)
	if err != nil {
		return fmt.Errorf("load grants: %w", err)
	}
	st.Grants, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Grant, error) {
		var g domain.Grant
		return g, row.Scan(&g.User, &g.Service, &g.Project, &g.Level)
	})
	if err != nil {
		return fmt.Errorf("load grants: %w", err)
	}
	return nil
}

func (s *Store) loadStock(ctx context.Context, st *domain.State) error {
	rows, err := s.pool.Query(ctx, `SELECT item, qty FROM stock`)
	if err != nil {
		return fmt.Errorf("load stock: %w", err)
	}
	defer rows.Close()
	st.Stock = map[string]int{}
	for rows.Next() {
		var item string
		var qty int
		if err := rows.Scan(&item, &qty); err != nil {
			return fmt.Errorf("load stock: %w", err)
		}
		st.Stock[item] = qty
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("load stock: %w", err)
	}
	return nil
}

func (s *Store) loadIssued(ctx context.Context, st *domain.State) error {
	rows, err := s.pool.Query(ctx, `SELECT user_id, item, returned FROM issued ORDER BY seq`)
	if err != nil {
		return fmt.Errorf("load issued: %w", err)
	}
	st.Issued, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Issued, error) {
		var is domain.Issued
		return is, row.Scan(&is.User, &is.Item, &is.Returned)
	})
	if err != nil {
		return fmt.Errorf("load issued: %w", err)
	}
	return nil
}

func (s *Store) loadSteps(ctx context.Context, st *domain.State) error {
	rows, err := s.pool.Query(ctx, `SELECT user_id, step, done_at, done_by FROM onboarding_done`)
	if err != nil {
		return fmt.Errorf("load onboarding: %w", err)
	}
	st.StepsDone, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.StepDone, error) {
		sd := domain.StepDone{Event: domain.Event{Status: "done"}}
		return sd, row.Scan(&sd.User, &sd.Step, &sd.Event.At, &sd.Event.By)
	})
	if err != nil {
		return fmt.Errorf("load onboarding: %w", err)
	}
	return nil
}

func (s *Store) loadIncidents(ctx context.Context, st *domain.State) error {
	rows, err := s.pool.Query(ctx, `SELECT id, service, title, message, since, declared_by, resolved_at FROM incidents`)
	if err != nil {
		return fmt.Errorf("load incidents: %w", err)
	}
	st.Incidents, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Incident, error) {
		var inc domain.Incident
		return inc, row.Scan(&inc.ID, &inc.Service, &inc.Title, &inc.Message, &inc.Since, &inc.By, &inc.ResolvedAt)
	})
	if err != nil {
		return fmt.Errorf("load incidents: %w", err)
	}
	return nil
}

func (s *Store) loadNotes(ctx context.Context, st *domain.State) error {
	rows, err := s.pool.Query(ctx, `SELECT id, user_id, kind, title, text, ref, created_at, read FROM notifications ORDER BY id`)
	if err != nil {
		return fmt.Errorf("load notifications: %w", err)
	}
	st.Notes, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Notification, error) {
		var n domain.Notification
		return n, row.Scan(&n.ID, &n.User, &n.Kind, &n.Title, &n.Text, &n.Ref, &n.CreatedAt, &n.Read)
	})
	if err != nil {
		return fmt.Errorf("load notifications: %w", err)
	}
	return nil
}

func (s *Store) loadSecurity(ctx context.Context, st *domain.State) error {
	rows, err := s.pool.Query(ctx, `SELECT id, user_id, name, kind, detail, at FROM security_events ORDER BY id`)
	if err != nil {
		return fmt.Errorf("load security events: %w", err)
	}
	st.Security, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.SecurityEvent, error) {
		var ev domain.SecurityEvent
		return ev, row.Scan(&ev.ID, &ev.User, &ev.Name, &ev.Kind, &ev.Detail, &ev.At)
	})
	if err != nil {
		return fmt.Errorf("load security events: %w", err)
	}
	return nil
}

func (s *Store) loadTopics(ctx context.Context, st *domain.State) error {
	rows, err := s.pool.Query(ctx, `SELECT user_id, topic, count FROM user_topics`)
	if err != nil {
		return fmt.Errorf("load topics: %w", err)
	}
	st.Topics, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.TopicStat, error) {
		var t domain.TopicStat
		return t, row.Scan(&t.User, &t.Topic, &t.Count)
	})
	if err != nil {
		return fmt.Errorf("load topics: %w", err)
	}
	return nil
}

func (s *Store) loadFlags(ctx context.Context, st *domain.State) error {
	rows, err := s.pool.Query(ctx, `SELECT user_id, starter_offered, kb_misses FROM user_flags`)
	if err != nil {
		return fmt.Errorf("load flags: %w", err)
	}
	st.Flags, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.UserFlags, error) {
		var f domain.UserFlags
		return f, row.Scan(&f.User, &f.StarterOffered, &f.KBMisses)
	})
	if err != nil {
		return fmt.Errorf("load flags: %w", err)
	}
	return nil
}

// Save - записывает пачку изменений одной транзакцией: либо все, либо ничего
func (s *Store) Save(ctx context.Context, ch domain.State) error {
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO meta (key, value) VALUES ('initialized', now()::text) ON CONFLICT (key) DO NOTHING`)
	for _, r := range ch.Requests {
		data, err := json.Marshal(r)
		if err != nil {
			return fmt.Errorf("encode request %s: %w", r.ID, err)
		}
		var due *time.Time
		if !r.DueAt.IsZero() {
			due = &r.DueAt
		}
		batch.Queue(`INSERT INTO requests (id, kind, user_id, status, created_at, updated_at, due_at, data)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (id) DO UPDATE SET status = EXCLUDED.status, updated_at = EXCLUDED.updated_at,
				due_at = EXCLUDED.due_at, data = EXCLUDED.data`,
			r.ID, r.Kind, r.User, r.Status, r.CreatedAt, r.UpdatedAt, due, data)
	}
	for _, g := range ch.Grants {
		batch.Queue(`INSERT INTO grants (user_id, service, project, level) VALUES ($1, $2, $3, $4)
			ON CONFLICT (user_id, service, project) DO UPDATE SET level = EXCLUDED.level`, g.User, g.Service, g.Project, g.Level)
	}
	for item, qty := range ch.Stock {
		batch.Queue(`INSERT INTO stock (item, qty) VALUES ($1, $2) ON CONFLICT (item) DO UPDATE SET qty = EXCLUDED.qty`, item, qty)
	}
	// новые выдачи дописываются в конец; транзакция атомарна, поэтому повтор после ошибки не задвоит их
	for _, is := range ch.Issued {
		batch.Queue(`INSERT INTO issued (user_id, item, returned) VALUES ($1, $2, $3)`, is.User, is.Item, is.Returned)
	}
	for _, sd := range ch.StepsDone {
		batch.Queue(`INSERT INTO onboarding_done (user_id, step, done_at, done_by) VALUES ($1, $2, $3, $4)
			ON CONFLICT (user_id, step) DO UPDATE SET done_at = EXCLUDED.done_at, done_by = EXCLUDED.done_by`,
			sd.User, sd.Step, sd.Event.At, sd.Event.By)
	}
	for _, inc := range ch.Incidents {
		batch.Queue(`INSERT INTO incidents (id, service, title, message, since, declared_by, resolved_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (id) DO UPDATE SET resolved_at = EXCLUDED.resolved_at`,
			inc.ID, inc.Service, inc.Title, inc.Message, inc.Since, inc.By, inc.ResolvedAt)
	}
	for _, n := range ch.Notes {
		batch.Queue(`INSERT INTO notifications (id, user_id, kind, title, text, ref, created_at, read)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (id) DO UPDATE SET read = EXCLUDED.read`,
			n.ID, n.User, n.Kind, n.Title, n.Text, n.Ref, n.CreatedAt, n.Read)
	}
	for _, ev := range ch.Security {
		batch.Queue(`INSERT INTO security_events (id, user_id, name, kind, detail, at) VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (id) DO NOTHING`, ev.ID, ev.User, ev.Name, ev.Kind, ev.Detail, ev.At)
	}
	for _, t := range ch.Topics {
		batch.Queue(`INSERT INTO user_topics (user_id, topic, count) VALUES ($1, $2, $3)
			ON CONFLICT (user_id, topic) DO UPDATE SET count = EXCLUDED.count`, t.User, t.Topic, t.Count)
	}
	for _, f := range ch.Flags {
		batch.Queue(`INSERT INTO user_flags (user_id, starter_offered, kb_misses) VALUES ($1, $2, $3)
			ON CONFLICT (user_id) DO UPDATE SET starter_offered = EXCLUDED.starter_offered, kb_misses = EXCLUDED.kb_misses`,
			f.User, f.StarterOffered, f.KBMisses)
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := tx.SendBatch(ctx, batch).Close(); err != nil {
			return fmt.Errorf("save changes: %w", err)
		}
		return nil
	})
}

// SyncDocs - база знаний из data/kb: добавить и обновить документы, удалить исчезнувшие
func (s *Store) SyncDocs(ctx context.Context, docs []domain.Doc) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		ids := make([]string, 0, len(docs))
		for _, d := range docs {
			ids = append(ids, d.ID)
			if _, err := tx.Exec(ctx, `INSERT INTO kb_docs (id, title, service, tags, body) VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title, service = EXCLUDED.service, tags = EXCLUDED.tags, body = EXCLUDED.body`,
				d.ID, d.Title, d.Service, strings.Join(d.Tags, " "), d.Body); err != nil {
				return fmt.Errorf("sync doc %s: %w", d.ID, err)
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM kb_docs WHERE NOT (id = ANY($1))`, ids); err != nil {
			return fmt.Errorf("delete stale docs: %w", err)
		}
		return nil
	})
}

const maxDocChars = 1500

// SearchKB - полнотекстовый поиск с русской морфологией. Каждое слово запроса ищется по основе
// с префиксом («подключается» найдет «подключиться»), слова объединяются через ИЛИ,
// документы с большим числом совпадений и совпадениями в заголовке выше.
func (s *Store) SearchKB(ctx context.Context, query string, limit int) ([]domain.DocHit, error) {
	rows, err := s.pool.Query(ctx, `
		WITH q AS (
			SELECT to_tsquery('russian', string_agg(quote_literal(left(lexeme, greatest(4, length(lexeme) - 2))) || ':*', ' | ')) AS tsq
			FROM unnest(to_tsvector('russian', $1))
		)
		SELECT d.id, d.title, d.service, ts_rank_cd(d.tsv, q.tsq) AS score, left(d.body, $3)
		FROM kb_docs d, q
		WHERE q.tsq IS NOT NULL AND d.tsv @@ q.tsq
		ORDER BY score DESC, d.id
		LIMIT $2`, query, limit, maxDocChars)
	if err != nil {
		return nil, fmt.Errorf("search kb: %w", err)
	}
	hits, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.DocHit, error) {
		var h domain.DocHit
		var score float32
		serr := row.Scan(&h.ID, &h.Title, &h.Service, &score, &h.Content)
		h.Score = float64(score)
		return h, serr
	})
	if err != nil {
		return nil, fmt.Errorf("search kb: %w", err)
	}
	return hits, nil
}
