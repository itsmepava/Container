-- MVP plan gaps: handling fee, invoice currency, no-bid fallback, payables, roll-over decision.
ALTER TABLE bookings ADD COLUMN handling_cents BIGINT;

ALTER TABLE invoices
  ADD COLUMN handling_cents BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN currency TEXT NOT NULL DEFAULT 'USD' CHECK (currency IN ('USD','LKR')),
  ADD COLUMN fx_rate NUMERIC(12,4) NOT NULL DEFAULT 1,
  ADD COLUMN total_local_cents BIGINT;

ALTER TABLE companies ADD COLUMN invoice_currency TEXT NOT NULL DEFAULT 'USD' CHECK (invoice_currency IN ('USD','LKR'));

ALTER TABLE lanes ADD COLUMN fallback_company_id BIGINT REFERENCES companies(id) ON DELETE SET NULL;
ALTER TABLE lane_box_rates ADD COLUMN fallback_rate NUMERIC(10,2) NOT NULL DEFAULT 0;

ALTER TABLE tenders
  ADD COLUMN payout_paid_at TIMESTAMPTZ,
  ADD COLUMN payout_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE tenders DROP CONSTRAINT tenders_award_kind_check;
ALTER TABLE tenders ADD CONSTRAINT tenders_award_kind_check CHECK (award_kind IN ('','auto','override','fallback','coload'));

ALTER TABLE sailings DROP CONSTRAINT sailings_decision_check;
ALTER TABLE sailings ADD CONSTRAINT sailings_decision_check CHECK (decision IN ('','own','coload','hybrid','roll'));
