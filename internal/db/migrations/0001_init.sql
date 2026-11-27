-- Eimsky schema v1

CREATE TABLE companies (
  id          BIGSERIAL PRIMARY KEY,
  kind        TEXT NOT NULL CHECK (kind IN ('factory','forwarder','association','eimsky')),
  name        TEXT NOT NULL,
  reg_number  TEXT NOT NULL DEFAULT '',
  address     TEXT NOT NULL DEFAULT '',
  phone       TEXT NOT NULL DEFAULT '',
  email       TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected','suspended')),
  status_note TEXT NOT NULL DEFAULT '',
  submitted_at TIMESTAMPTZ,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE users (
  id             BIGSERIAL PRIMARY KEY,
  company_id     BIGINT REFERENCES companies(id),
  role           TEXT NOT NULL CHECK (role IN ('admin','ops','factory_admin','factory_staff','forwarder','viewer')),
  name           TEXT NOT NULL,
  email          TEXT NOT NULL DEFAULT '',
  phone          TEXT NOT NULL DEFAULT '',
  password_hash  TEXT NOT NULL DEFAULT '',
  active         BOOLEAN NOT NULL DEFAULT true,
  email_notify   BOOLEAN NOT NULL DEFAULT true,
  last_login_at  TIMESTAMPTZ,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_email_uq ON users (lower(email)) WHERE email <> '';
CREATE UNIQUE INDEX users_phone_uq ON users (phone) WHERE phone <> '';

CREATE TABLE sessions (
  token_hash  TEXT PRIMARY KEY,
  user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind        TEXT NOT NULL CHECK (kind IN ('web','api')),
  csrf        TEXT NOT NULL,
  user_agent  TEXT NOT NULL DEFAULT '',
  expires_at  TIMESTAMPTZ NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX sessions_user ON sessions(user_id);

-- One-time tokens: password reset, invites, phone OTP.
CREATE TABLE one_time_tokens (
  id          BIGSERIAL PRIMARY KEY,
  kind        TEXT NOT NULL CHECK (kind IN ('reset','invite','otp')),
  user_id     BIGINT REFERENCES users(id) ON DELETE CASCADE,
  phone       TEXT NOT NULL DEFAULT '',
  token_hash  TEXT NOT NULL,
  attempts    INT NOT NULL DEFAULT 0,
  expires_at  TIMESTAMPTZ NOT NULL,
  used_at     TIMESTAMPTZ,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ott_hash ON one_time_tokens(token_hash);
CREATE INDEX ott_phone ON one_time_tokens(phone) WHERE kind = 'otp';

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE ports (
  code    TEXT PRIMARY KEY,
  name    TEXT NOT NULL,
  country TEXT NOT NULL
);

CREATE TABLE container_types (
  code          TEXT PRIMARY KEY,
  name          TEXT NOT NULL,
  internal_cbm  NUMERIC(8,2) NOT NULL,
  payload_kg    NUMERIC(10,1) NOT NULL,
  sort          INT NOT NULL DEFAULT 0,
  active        BOOLEAN NOT NULL DEFAULT true
);

CREATE TABLE lanes (
  id                BIGSERIAL PRIMARY KEY,
  origin            TEXT NOT NULL REFERENCES ports(code),
  dest              TEXT NOT NULL REFERENCES ports(code),
  active            BOOLEAN NOT NULL DEFAULT true,
  coload_rate       NUMERIC(10,2) NOT NULL DEFAULT 55,   -- USD per W/M
  estimate_per_wm   NUMERIC(10,2) NOT NULL DEFAULT 50,   -- booking estimate cap, USD per W/M
  origin_cfs        TEXT NOT NULL DEFAULT '',
  default_box       TEXT NOT NULL DEFAULT '40HC' REFERENCES container_types(code),
  default_grade     TEXT NOT NULL DEFAULT 'A',
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (origin, dest)
);

CREATE TABLE lane_box_rates (
  lane_id       BIGINT NOT NULL REFERENCES lanes(id) ON DELETE CASCADE,
  box_code      TEXT NOT NULL REFERENCES container_types(code),
  expected_bid  NUMERIC(10,2) NOT NULL,
  PRIMARY KEY (lane_id, box_code)
);

CREATE TABLE sailings (
  id                BIGSERIAL PRIMARY KEY,
  lane_id           BIGINT NOT NULL REFERENCES lanes(id),
  year              INT NOT NULL,
  week              INT NOT NULL,
  cfs_cutoff        TIMESTAMPTZ NOT NULL,
  plan_lock_at      TIMESTAMPTZ NOT NULL,
  bid_close_at      TIMESTAMPTZ NOT NULL,
  status            TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','tendering','awarded','coload','closed','cancelled')),
  decision          TEXT NOT NULL DEFAULT '' CHECK (decision IN ('','own','coload','hybrid')),
  chosen_vessel_id  BIGINT,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (lane_id, year, week)
);

CREATE TABLE vessel_options (
  id             BIGSERIAL PRIMARY KEY,
  sailing_id     BIGINT NOT NULL REFERENCES sailings(id) ON DELETE CASCADE,
  name           TEXT NOT NULL,
  voyage         TEXT NOT NULL DEFAULT '',
  service        TEXT NOT NULL DEFAULT 'standard' CHECK (service IN ('standard','fast')),
  etd            TIMESTAMPTZ NOT NULL,
  eta            TIMESTAMPTZ NOT NULL,
  via            TEXT NOT NULL DEFAULT '',
  extra_per_box  NUMERIC(10,2) NOT NULL DEFAULT 0,
  is_default     BOOLEAN NOT NULL DEFAULT false
);
ALTER TABLE sailings ADD CONSTRAINT sailings_vessel_fk FOREIGN KEY (chosen_vessel_id) REFERENCES vessel_options(id) ON DELETE SET NULL;

CREATE TABLE containers (
  id          BIGSERIAL PRIMARY KEY,
  sailing_id  BIGINT NOT NULL REFERENCES sailings(id) ON DELETE CASCADE,
  seq         INT NOT NULL,
  box_code    TEXT NOT NULL REFERENCES container_types(code),
  grade       TEXT NOT NULL CHECK (grade IN ('A','B','C')),
  status      TEXT NOT NULL DEFAULT 'planning' CHECK (status IN ('planning','tendering','awarded','cancelled')),
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (sailing_id, seq)
);

CREATE SEQUENCE booking_ref_seq START 101;
CREATE TABLE bookings (
  id               BIGSERIAL PRIMARY KEY,
  ref              TEXT NOT NULL UNIQUE,
  company_id       BIGINT NOT NULL REFERENCES companies(id),
  created_by       BIGINT NOT NULL REFERENCES users(id),
  sailing_id       BIGINT NOT NULL REFERENCES sailings(id),
  box_code         TEXT NOT NULL REFERENCES container_types(code),
  category         TEXT NOT NULL,
  pieces           INT NOT NULL,
  cbm              NUMERIC(10,3) NOT NULL,
  kg               NUMERIC(12,1) NOT NULL,
  wm               NUMERIC(10,3) NOT NULL,
  cap_cents        BIGINT NOT NULL,
  status           TEXT NOT NULL CHECK (status IN ('held','booked','cancelled','expired')),
  hold_expires_at  TIMESTAMPTZ,
  container_id     BIGINT REFERENCES containers(id) ON DELETE SET NULL,
  mode             TEXT NOT NULL DEFAULT 'pool' CHECK (mode IN ('pool','coload')),
  freight_cents    BIGINT,
  fee_cents        BIGINT,
  total_cents      BIGINT,
  cap_applied      BOOLEAN NOT NULL DEFAULT false,
  priced_at        TIMESTAMPTZ,
  note             TEXT NOT NULL DEFAULT '',
  confirmed_at     TIMESTAMPTZ,
  cancelled_at     TIMESTAMPTZ,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX bookings_sailing ON bookings(sailing_id);
CREATE INDEX bookings_company ON bookings(company_id);

CREATE TABLE booking_lines (
  id          BIGSERIAL PRIMARY KEY,
  booking_id  BIGINT NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
  line_no     INT NOT NULL,
  hs_code     TEXT NOT NULL,
  pieces      INT NOT NULL,
  length_cm   NUMERIC(8,1) NOT NULL,
  width_cm    NUMERIC(8,1) NOT NULL,
  height_cm   NUMERIC(8,1) NOT NULL,
  kg_each     NUMERIC(10,2) NOT NULL,
  cbm         NUMERIC(10,3) NOT NULL,
  kg          NUMERIC(12,1) NOT NULL
);

CREATE TABLE move_requests (
  id              BIGSERIAL PRIMARY KEY,
  booking_id      BIGINT NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
  from_sailing_id BIGINT NOT NULL REFERENCES sailings(id),
  target          TEXT NOT NULL CHECK (target IN ('next_week','extra_box','coload')),
  to_sailing_id   BIGINT REFERENCES sailings(id),
  reason          TEXT NOT NULL,
  status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','accepted','declined','cancelled')),
  requested_by    BIGINT REFERENCES users(id),
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  resolved_at     TIMESTAMPTZ
);

CREATE SEQUENCE tender_ref_seq START 21;
CREATE TABLE tenders (
  id              BIGSERIAL PRIMARY KEY,
  ref             TEXT NOT NULL UNIQUE,
  container_id    BIGINT NOT NULL UNIQUE REFERENCES containers(id),
  vessel_id       BIGINT NOT NULL REFERENCES vessel_options(id),
  opens_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  closes_at       TIMESTAMPTZ NOT NULL,
  status          TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','closed','awarded','cancelled')),
  awarded_bid_id  BIGINT,
  award_kind      TEXT NOT NULL DEFAULT '' CHECK (award_kind IN ('','auto','override')),
  award_reason    TEXT NOT NULL DEFAULT '',
  awarded_by      BIGINT REFERENCES users(id),
  awarded_at      TIMESTAMPTZ,
  handover_at     TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE forwarder_lanes (
  company_id BIGINT NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  lane_id    BIGINT NOT NULL REFERENCES lanes(id) ON DELETE CASCADE,
  PRIMARY KEY (company_id, lane_id)
);

CREATE TABLE tender_invites (
  tender_id   BIGINT NOT NULL REFERENCES tenders(id) ON DELETE CASCADE,
  company_id  BIGINT NOT NULL REFERENCES companies(id),
  invited_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (tender_id, company_id)
);

CREATE TABLE bids (
  id               BIGSERIAL PRIMARY KEY,
  tender_id        BIGINT NOT NULL REFERENCES tenders(id) ON DELETE CASCADE,
  company_id       BIGINT NOT NULL REFERENCES companies(id),
  user_id          BIGINT NOT NULL REFERENCES users(id),
  amount_cents     BIGINT NOT NULL CHECK (amount_cents > 0),
  vessel_id        BIGINT NOT NULL REFERENCES vessel_options(id),
  inc_ocean        BOOLEAN NOT NULL,
  inc_origin       BOOLEAN NOT NULL,
  inc_destination  BOOLEAN NOT NULL,
  inc_destuff      BOOLEAN NOT NULL,
  note             TEXT NOT NULL DEFAULT '',
  submitted_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (tender_id, company_id)
);
ALTER TABLE tenders ADD CONSTRAINT tenders_bid_fk FOREIGN KEY (awarded_bid_id) REFERENCES bids(id);

CREATE TABLE documents (
  id            BIGSERIAL PRIMARY KEY,
  owner_kind    TEXT NOT NULL CHECK (owner_kind IN ('booking','company','tender')),
  owner_id      BIGINT NOT NULL,
  kind          TEXT NOT NULL,
  filename      TEXT NOT NULL,
  content_type  TEXT NOT NULL,
  size_bytes    BIGINT NOT NULL,
  storage_key   TEXT NOT NULL,
  uploaded_by   BIGINT REFERENCES users(id),
  uploaded_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  purged_at     TIMESTAMPTZ
);
CREATE INDEX documents_owner ON documents(owner_kind, owner_id);

CREATE SEQUENCE invoice_seq START 1001;
CREATE TABLE invoices (
  id             BIGSERIAL PRIMARY KEY,
  number         TEXT NOT NULL UNIQUE,
  booking_id     BIGINT NOT NULL UNIQUE REFERENCES bookings(id),
  company_id     BIGINT NOT NULL REFERENCES companies(id),
  freight_cents  BIGINT NOT NULL,
  fee_cents      BIGINT NOT NULL,
  total_cents    BIGINT NOT NULL,
  status         TEXT NOT NULL DEFAULT 'issued' CHECK (status IN ('issued','paid','void')),
  issued_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  paid_at        TIMESTAMPTZ
);

CREATE TABLE notifications (
  id          BIGSERIAL PRIMARY KEY,
  user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  title       TEXT NOT NULL,
  body        TEXT NOT NULL DEFAULT '',
  link        TEXT NOT NULL DEFAULT '',
  read_at     TIMESTAMPTZ,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX notifications_user ON notifications(user_id, created_at DESC);

CREATE TABLE push_tokens (
  token       TEXT PRIMARY KEY,
  user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  platform    TEXT NOT NULL DEFAULT '',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE email_outbox (
  id          BIGSERIAL PRIMARY KEY,
  to_addr     TEXT NOT NULL,
  subject     TEXT NOT NULL,
  body        TEXT NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  sent_at     TIMESTAMPTZ,
  attempts    INT NOT NULL DEFAULT 0,
  last_error  TEXT NOT NULL DEFAULT ''
);

CREATE TABLE audit_log (
  id          BIGSERIAL PRIMARY KEY,
  at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  actor_id    BIGINT REFERENCES users(id),
  actor_name  TEXT NOT NULL DEFAULT 'system',
  action      TEXT NOT NULL,
  entity      TEXT NOT NULL DEFAULT '',
  entity_id   BIGINT,
  summary     TEXT NOT NULL DEFAULT '',
  detail      JSONB NOT NULL DEFAULT '{}'
);
CREATE INDEX audit_entity ON audit_log(entity, entity_id);
CREATE INDEX audit_at ON audit_log(at DESC);

CREATE TABLE job_marks (
  key  TEXT PRIMARY KEY,
  at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE hs_codes (
  code        TEXT PRIMARY KEY,
  description TEXT NOT NULL
);
