-- Состояние ядра AI IT-Buddy. Справочники (сотрудники, каталог, роли, шаги) живут в YAML,
-- здесь - только то, что меняется в работе.

CREATE TABLE meta (
    key   text PRIMARY KEY,
    value text NOT NULL
);

-- Заявки: ключевые поля колонками (фильтры и индексы), детали и история - jsonb
CREATE TABLE requests (
    id         text PRIMARY KEY,
    kind       text        NOT NULL,
    user_id    text        NOT NULL,
    status     text        NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    due_at     timestamptz,
    data       jsonb       NOT NULL
);
CREATE INDEX requests_user_idx ON requests (user_id, created_at DESC);
CREATE INDEX requests_open_idx ON requests (status) WHERE status NOT IN ('approved', 'rejected', 'ready', 'cancelled');

CREATE TABLE grants (
    user_id text NOT NULL,
    service text NOT NULL,
    project text NOT NULL,
    level   text NOT NULL,
    PRIMARY KEY (user_id, service, project)
);

CREATE TABLE stock (
    item text PRIMARY KEY,
    qty  int  NOT NULL CHECK (qty >= 0)
);

-- Выдачи только добавляются, порядок важен (возврат забирает последнюю выдачу)
CREATE TABLE issued (
    seq     bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id text NOT NULL,
    item    text NOT NULL
);

CREATE TABLE onboarding_done (
    user_id text        NOT NULL,
    step    text        NOT NULL,
    done_at timestamptz NOT NULL,
    done_by text        NOT NULL,
    PRIMARY KEY (user_id, step)
);

CREATE TABLE incidents (
    id          text PRIMARY KEY,
    service     text        NOT NULL,
    title       text        NOT NULL,
    message     text        NOT NULL DEFAULT '',
    since       timestamptz NOT NULL,
    declared_by text        NOT NULL DEFAULT '',
    resolved_at timestamptz
);

CREATE TABLE notifications (
    id         bigint PRIMARY KEY,
    user_id    text        NOT NULL,
    kind       text        NOT NULL,
    title      text        NOT NULL,
    text       text        NOT NULL,
    ref        text        NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL,
    read       boolean     NOT NULL DEFAULT false
);
CREATE INDEX notifications_user_idx ON notifications (user_id, id);

CREATE TABLE security_events (
    id      int PRIMARY KEY,
    user_id text        NOT NULL,
    name    text        NOT NULL,
    kind    text        NOT NULL,
    detail  text        NOT NULL,
    at      timestamptz NOT NULL
);
CREATE INDEX security_events_user_idx ON security_events (user_id, at DESC);

-- Темы, с которыми стажер приходил к помощнику: только счетчики, текст переписки не храним
CREATE TABLE user_topics (
    user_id text NOT NULL,
    topic   text NOT NULL,
    count   int  NOT NULL,
    PRIMARY KEY (user_id, topic)
);

CREATE TABLE user_flags (
    user_id         text PRIMARY KEY,
    starter_offered boolean NOT NULL DEFAULT false,
    kb_misses       int     NOT NULL DEFAULT 0
);

-- База знаний: синхронизируется из data/kb при старте, поиск полнотекстовый с русской морфологией.
-- Заголовок и теги весят больше текста.
CREATE TABLE kb_docs (
    id      text PRIMARY KEY,
    title   text NOT NULL,
    service text NOT NULL DEFAULT '',
    tags    text NOT NULL DEFAULT '',
    body    text NOT NULL,
    tsv     tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('russian', title), 'A') ||
        setweight(to_tsvector('russian', tags), 'A') ||
        setweight(to_tsvector('russian', service), 'B') ||
        setweight(to_tsvector('russian', body), 'C')
    ) STORED
);
CREATE INDEX kb_docs_tsv_idx ON kb_docs USING gin (tsv);
