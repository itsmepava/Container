-- Load planner: container options per lane (type, grade, all-in cost) and handling cost per W/M.
CREATE TABLE lane_options (
  id         BIGSERIAL PRIMARY KEY,
  lane_id    BIGINT NOT NULL REFERENCES lanes(id) ON DELETE CASCADE,
  box_code   TEXT NOT NULL,
  grade      TEXT NOT NULL CHECK (grade IN ('A','B','C')),
  cost       NUMERIC(10,2) NOT NULL CHECK (cost > 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (lane_id, box_code, grade)
);
CREATE INDEX lane_options_lane ON lane_options(lane_id);

ALTER TABLE lanes ADD COLUMN handling_per_wm NUMERIC(10,2) NOT NULL DEFAULT 5;

INSERT INTO lane_options(lane_id, box_code, grade, cost)
SELECT r.lane_id, r.box_code, l.default_grade, r.expected_bid
FROM lane_box_rates r JOIN lanes l ON l.id = r.lane_id
WHERE r.expected_bid > 0
ON CONFLICT DO NOTHING;
