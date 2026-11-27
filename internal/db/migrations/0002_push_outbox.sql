CREATE TABLE push_outbox (
  id          BIGSERIAL PRIMARY KEY,
  user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  title       TEXT NOT NULL,
  body        TEXT NOT NULL,
  data        JSONB NOT NULL DEFAULT '{}',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  sent_at     TIMESTAMPTZ,
  attempts    INT NOT NULL DEFAULT 0,
  last_error  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX push_outbox_pending ON push_outbox(id) WHERE sent_at IS NULL;
CREATE INDEX email_outbox_pending ON email_outbox(id) WHERE sent_at IS NULL;
