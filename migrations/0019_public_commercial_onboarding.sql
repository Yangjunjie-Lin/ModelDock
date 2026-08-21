-- Public commercial catalog, onboarding evidence, and acquisition funnel.
--
-- This migration is additive and forward-only.  It does not change /v1,
-- RelayDock API key formats, existing prices, wallet balances, or historical
-- request records.  Funnel milestones are derived from authoritative business
-- rows inside the same PostgreSQL transaction that creates those rows.

CREATE TABLE IF NOT EXISTS public_commercial_terms (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  idempotency_key text NOT NULL UNIQUE CHECK (length(idempotency_key) BETWEEN 1 AND 200),
  request_fingerprint text NOT NULL CHECK (length(request_fingerprint)=64),
  region text NOT NULL CHECK (region='*' OR region ~ '^[A-Z]{2}$'),
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  subscription_tax_included boolean,
  token_tax_included boolean,
  tax_disclosure text NOT NULL DEFAULT '',
  refund_summary text NOT NULL DEFAULT '',
  refund_policy_url text NOT NULL DEFAULT '/legal/refunds',
  bonus_credit_amount numeric(30,12) NOT NULL DEFAULT 0 CHECK (bonus_credit_amount >= 0),
  bonus_non_refundable boolean NOT NULL DEFAULT true,
  effective_at timestamptz NOT NULL,
  expires_at timestamptz,
  legal_review_required boolean NOT NULL DEFAULT true CHECK (legal_review_required),
  legal_review_status text NOT NULL DEFAULT 'PENDING'
    CHECK (legal_review_status IN ('PENDING','APPROVED')),
  reviewed_by uuid REFERENCES users(id) ON DELETE SET NULL,
  reviewed_at timestamptz,
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (expires_at IS NULL OR expires_at > effective_at),
  CHECK ((legal_review_status='PENDING' AND reviewed_at IS NULL)
      OR (legal_review_status='APPROVED' AND reviewed_at IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS public_commercial_terms_lookup_idx
  ON public_commercial_terms(region,currency,effective_at DESC,id DESC);

CREATE TABLE IF NOT EXISTS public_payment_fee_schedule (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  idempotency_key text NOT NULL UNIQUE CHECK (length(idempotency_key) BETWEEN 1 AND 200),
  request_fingerprint text NOT NULL CHECK (length(request_fingerprint)=64),
  fee_category text NOT NULL CHECK (fee_category IN ('PAYMENT_CHANNEL','PLATFORM_SERVICE')),
  payment_provider text NOT NULL CHECK (payment_provider ~ '^[a-z][a-z0-9_-]{1,31}$'),
  region text NOT NULL CHECK (region='*' OR region ~ '^[A-Z]{2}$'),
  currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  fee_kind text NOT NULL CHECK (fee_kind IN ('NONE','FIXED','PERCENT','FIXED_PLUS_PERCENT')),
  fixed_amount numeric(30,12) NOT NULL DEFAULT 0 CHECK (fixed_amount >= 0),
  rate_bps integer NOT NULL DEFAULT 0 CHECK (rate_bps BETWEEN 0 AND 100000),
  charged_to_customer boolean NOT NULL DEFAULT false,
  description text NOT NULL DEFAULT '',
  effective_at timestamptz NOT NULL,
  expires_at timestamptz,
  legal_review_required boolean NOT NULL DEFAULT true CHECK (legal_review_required),
  legal_review_status text NOT NULL DEFAULT 'PENDING'
    CHECK (legal_review_status IN ('PENDING','APPROVED')),
  reviewed_by uuid REFERENCES users(id) ON DELETE SET NULL,
  reviewed_at timestamptz,
  created_by uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (expires_at IS NULL OR expires_at > effective_at),
  CHECK (
    (fee_kind='NONE' AND fixed_amount=0 AND rate_bps=0) OR
    (fee_kind='FIXED' AND fixed_amount>0 AND rate_bps=0) OR
    (fee_kind='PERCENT' AND fixed_amount=0 AND rate_bps>0) OR
    (fee_kind='FIXED_PLUS_PERCENT' AND fixed_amount>0 AND rate_bps>0)
  ),
  CHECK ((legal_review_status='PENDING' AND reviewed_at IS NULL)
      OR (legal_review_status='APPROVED' AND reviewed_at IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS public_payment_fee_schedule_lookup_idx
  ON public_payment_fee_schedule(region,currency,payment_provider,fee_category,effective_at DESC,id DESC);

-- Published terms and fee schedules are versioned evidence.  Corrections are
-- appended with a new effective period instead of rewriting a disclosure a
-- customer may already have relied on.
CREATE OR REPLACE FUNCTION reject_public_commercial_evidence_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION '% records are immutable; append a replacement version', TG_TABLE_NAME
    USING ERRCODE='55000';
END;
$$;
DROP TRIGGER IF EXISTS public_commercial_terms_immutable_trigger ON public_commercial_terms;
CREATE TRIGGER public_commercial_terms_immutable_trigger
  BEFORE UPDATE OR DELETE ON public_commercial_terms
  FOR EACH ROW EXECUTE FUNCTION reject_public_commercial_evidence_mutation();
DROP TRIGGER IF EXISTS public_payment_fee_schedule_immutable_trigger ON public_payment_fee_schedule;
CREATE TRIGGER public_payment_fee_schedule_immutable_trigger
  BEFORE UPDATE OR DELETE ON public_payment_fee_schedule
  FOR EACH ROW EXECUTE FUNCTION reject_public_commercial_evidence_mutation();

CREATE TABLE IF NOT EXISTS commercial_funnel_events (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  event_type text NOT NULL CHECK (event_type IN (
    'HOMEPAGE_VISITED','REGISTERED','EMAIL_VERIFIED','API_KEY_CREATED',
    'FIRST_RECHARGE','FIRST_API_CALL','SECOND_API_CALL','FIRST_SUBSCRIPTION'
  )),
  user_id uuid REFERENCES users(id) ON DELETE RESTRICT,
  organization_id uuid REFERENCES organizations(id) ON DELETE RESTRICT,
  anonymous_id_hash bytea,
  source_resource_type text NOT NULL,
  source_resource_id text NOT NULL,
  idempotency_key text NOT NULL UNIQUE CHECK (length(idempotency_key) BETWEEN 1 AND 200),
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata)='object'),
  occurred_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (
    (event_type='HOMEPAGE_VISITED' AND anonymous_id_hash IS NOT NULL
      AND user_id IS NULL AND organization_id IS NULL)
    OR
    (event_type IN ('REGISTERED','EMAIL_VERIFIED','API_KEY_CREATED','FIRST_API_CALL','SECOND_API_CALL')
      AND user_id IS NOT NULL AND anonymous_id_hash IS NULL)
    OR
    (event_type IN ('FIRST_RECHARGE','FIRST_SUBSCRIPTION')
      AND organization_id IS NOT NULL AND anonymous_id_hash IS NULL)
  )
);
CREATE INDEX IF NOT EXISTS commercial_funnel_events_type_occurred_idx
  ON commercial_funnel_events(event_type,occurred_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS commercial_funnel_events_user_occurred_idx
  ON commercial_funnel_events(user_id,occurred_at DESC,id DESC) WHERE user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS commercial_funnel_events_org_occurred_idx
  ON commercial_funnel_events(organization_id,occurred_at DESC,id DESC) WHERE organization_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS commercial_funnel_events_user_milestone_uq
  ON commercial_funnel_events(user_id,event_type)
  WHERE user_id IS NOT NULL AND event_type IN (
    'REGISTERED','EMAIL_VERIFIED','API_KEY_CREATED','FIRST_API_CALL','SECOND_API_CALL'
  );
CREATE UNIQUE INDEX IF NOT EXISTS commercial_funnel_events_org_milestone_uq
  ON commercial_funnel_events(organization_id,event_type)
  WHERE organization_id IS NOT NULL AND event_type IN ('FIRST_RECHARGE','FIRST_SUBSCRIPTION');

CREATE TABLE IF NOT EXISTS commercial_funnel_api_call_counter (
  user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE RESTRICT,
  successful_call_count bigint NOT NULL DEFAULT 0 CHECK (successful_call_count >= 0),
  first_request_id text,
  second_request_id text,
  updated_at timestamptz NOT NULL DEFAULT now()
);

-- Upgrade backfill.  Only the earliest durable fact for each milestone is
-- copied.  No prompt, response, email address, API key, IP address, or payment
-- credential is copied into analytics storage.
WITH registrations AS (
  SELECT DISTINCT ON (u.id) u.id user_id,a.id audit_id,a.created_at,
    CASE WHEN EXISTS (
      SELECT 1 FROM organization_invitations invitation WHERE invitation.accepted_by=u.id
    ) THEN 'INVITATION' ELSE 'SELF_REGISTRATION' END acquisition_source
  FROM audit_logs a JOIN users u ON u.id=a.actor_id
  WHERE a.action='security.registration_created' AND a.resource_type='user'
    AND a.resource_id=u.id::text
  ORDER BY u.id,a.created_at,a.id
)
INSERT INTO commercial_funnel_events(
  event_type,user_id,source_resource_type,source_resource_id,idempotency_key,metadata,occurred_at
)
SELECT 'REGISTERED',r.user_id,'audit_log',r.audit_id::text,
  'funnel:registered:'||r.user_id::text,
  jsonb_build_object('acquisition_source',r.acquisition_source),r.created_at
FROM registrations r ON CONFLICT DO NOTHING;

INSERT INTO commercial_funnel_events(
  event_type,user_id,source_resource_type,source_resource_id,idempotency_key,occurred_at
)
SELECT 'EMAIL_VERIFIED',u.id,'user',u.id::text,'funnel:email_verified:'||u.id::text,u.email_verified_at
FROM users u JOIN commercial_funnel_events registration
  ON registration.user_id=u.id AND registration.event_type='REGISTERED'
WHERE u.email_verified_at IS NOT NULL
ON CONFLICT DO NOTHING;

WITH first_key AS (
  SELECT DISTINCT ON (k.user_id) k.user_id,k.id,k.created_at
  FROM api_keys k ORDER BY k.user_id,k.created_at,k.id
)
INSERT INTO commercial_funnel_events(
  event_type,user_id,organization_id,source_resource_type,source_resource_id,idempotency_key,occurred_at
)
SELECT 'API_KEY_CREATED',k.user_id,a.organization_id,'api_key',k.id::text,
  'funnel:api_key_created:'||k.user_id::text,k.created_at
FROM first_key k JOIN api_keys a ON a.id=k.id
JOIN commercial_funnel_events registration
  ON registration.user_id=k.user_id AND registration.event_type='REGISTERED'
ON CONFLICT DO NOTHING;

WITH first_recharge AS (
  SELECT DISTINCT ON (r.organization_id) r.organization_id,r.id,r.created_by,
    COALESCE(r.credited_at,r.paid_at,r.updated_at,r.created_at) occurred_at
  FROM recharge_order r WHERE r.status IN ('CREDITED','REFUND_PENDING','REFUNDED','CHARGEBACK')
  ORDER BY r.organization_id,COALESCE(r.credited_at,r.paid_at,r.updated_at,r.created_at),r.id
)
INSERT INTO commercial_funnel_events(
  event_type,user_id,organization_id,source_resource_type,source_resource_id,idempotency_key,occurred_at
)
SELECT 'FIRST_RECHARGE',r.created_by,r.organization_id,'recharge_order',r.id::text,
  'funnel:first_recharge:'||r.organization_id::text,r.occurred_at
FROM first_recharge r
JOIN commercial_funnel_events registration
  ON registration.user_id=r.created_by AND registration.event_type='REGISTERED'
ON CONFLICT DO NOTHING;

WITH ranked_calls AS (
  SELECT l.user_id,l.organization_id,l.request_id,l.created_at,
    row_number() OVER(PARTITION BY l.user_id ORDER BY l.created_at,l.id) call_number
  FROM request_logs l
  JOIN commercial_funnel_events registration
    ON registration.user_id=l.user_id AND registration.event_type='REGISTERED'
  LEFT JOIN funding_operation funding ON funding.id=l.funding_operation_id
  WHERE l.user_id IS NOT NULL AND l.status_code BETWEEN 200 AND 299
    AND (l.funding_operation_id IS NULL OR funding.status IN ('SETTLED','PARTIALLY_SETTLED','RELEASED'))
)
INSERT INTO commercial_funnel_events(
  event_type,user_id,organization_id,source_resource_type,source_resource_id,idempotency_key,occurred_at
)
SELECT CASE r.call_number WHEN 1 THEN 'FIRST_API_CALL' ELSE 'SECOND_API_CALL' END,
  r.user_id,r.organization_id,'request_log',r.request_id,
  CASE r.call_number WHEN 1 THEN 'funnel:first_api_call:' ELSE 'funnel:second_api_call:' END||r.user_id::text,
  r.created_at
FROM ranked_calls r WHERE r.call_number IN (1,2)
ON CONFLICT DO NOTHING;

INSERT INTO commercial_funnel_api_call_counter(user_id,successful_call_count,first_request_id,second_request_id,updated_at)
SELECT l.user_id,count(*),
  (array_agg(l.request_id ORDER BY l.created_at,l.id))[1],
  (array_agg(l.request_id ORDER BY l.created_at,l.id))[2],now()
FROM request_logs l
JOIN commercial_funnel_events registration
  ON registration.user_id=l.user_id AND registration.event_type='REGISTERED'
LEFT JOIN funding_operation funding ON funding.id=l.funding_operation_id
WHERE l.user_id IS NOT NULL AND l.status_code BETWEEN 200 AND 299
  AND (l.funding_operation_id IS NULL OR funding.status IN ('SETTLED','PARTIALLY_SETTLED','RELEASED'))
GROUP BY l.user_id
ON CONFLICT(user_id) DO UPDATE SET
  successful_call_count=EXCLUDED.successful_call_count,
  first_request_id=EXCLUDED.first_request_id,
  second_request_id=EXCLUDED.second_request_id,
  updated_at=now();

WITH first_subscription AS (
  SELECT DISTINCT ON (event.organization_id) event.organization_id,event.id,event.actor_id,
    event.organization_subscription_id,event.created_at
  FROM subscription_event event
  JOIN commercial_funnel_events registration
    ON registration.user_id=event.actor_id AND registration.event_type='REGISTERED'
  WHERE event.event_type IN ('SUBSCRIPTION_CREATED','PLAN_CHANGED_IMMEDIATELY','PLAN_CHANGE_SCHEDULED')
  ORDER BY event.organization_id,event.created_at,event.id
)
INSERT INTO commercial_funnel_events(
  event_type,user_id,organization_id,source_resource_type,source_resource_id,idempotency_key,occurred_at
)
SELECT 'FIRST_SUBSCRIPTION',s.actor_id,s.organization_id,'subscription_event',s.id::text,
  'funnel:first_subscription:'||s.organization_id::text,s.created_at
FROM first_subscription s
ON CONFLICT DO NOTHING;

CREATE OR REPLACE FUNCTION record_commercial_funnel_event(
  p_event_type text,
  p_user_id uuid,
  p_organization_id uuid,
  p_anonymous_id_hash bytea,
  p_source_resource_type text,
  p_source_resource_id text,
  p_idempotency_key text,
  p_occurred_at timestamptz,
  p_metadata jsonb DEFAULT '{}'::jsonb
) RETURNS uuid LANGUAGE plpgsql AS $$
DECLARE
  inserted_id uuid;
BEGIN
  INSERT INTO commercial_funnel_events(
    id,event_type,user_id,organization_id,anonymous_id_hash,source_resource_type,
    source_resource_id,idempotency_key,metadata,occurred_at
  ) VALUES(
    gen_random_uuid(),p_event_type,p_user_id,p_organization_id,p_anonymous_id_hash,
    p_source_resource_type,p_source_resource_id,p_idempotency_key,COALESCE(p_metadata,'{}'::jsonb),p_occurred_at
  )
  ON CONFLICT DO NOTHING
  RETURNING id INTO inserted_id;

  -- The immutable/idempotent event row is the analytics evidence.  Do not
  -- duplicate high-volume public traffic into audit_logs: its tamper-evident
  -- hash chain deliberately serializes privileged audit writes.
  RETURN inserted_id;
END;
$$;

CREATE OR REPLACE FUNCTION reject_commercial_funnel_event_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'commercial funnel events are immutable' USING ERRCODE='55000';
END;
$$;
DROP TRIGGER IF EXISTS commercial_funnel_events_immutable_trigger ON commercial_funnel_events;
CREATE TRIGGER commercial_funnel_events_immutable_trigger
  BEFORE UPDATE OR DELETE ON commercial_funnel_events
  FOR EACH ROW EXECUTE FUNCTION reject_commercial_funnel_event_mutation();

CREATE OR REPLACE FUNCTION commercial_funnel_user_verification_trigger()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  acquisition_source text;
BEGIN
  SELECT registration.metadata->>'acquisition_source' INTO acquisition_source
  FROM commercial_funnel_events registration
  WHERE registration.user_id=NEW.id AND registration.event_type='REGISTERED';
  IF acquisition_source IS NOT NULL
     AND OLD.email_verified_at IS NULL AND NEW.email_verified_at IS NOT NULL THEN
    PERFORM record_commercial_funnel_event('EMAIL_VERIFIED',NEW.id,NULL,NULL,'user',NEW.id::text,
      'funnel:email_verified:'||NEW.id::text,NEW.email_verified_at,
      jsonb_build_object('acquisition_source',acquisition_source));
  END IF;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS commercial_funnel_user_verification ON users;
CREATE TRIGGER commercial_funnel_user_verification
  AFTER UPDATE OF email_verified_at ON users FOR EACH ROW
  EXECUTE FUNCTION commercial_funnel_user_verification_trigger();

CREATE OR REPLACE FUNCTION commercial_funnel_api_key_trigger()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  acquisition_source text;
BEGIN
  SELECT registration.metadata->>'acquisition_source' INTO acquisition_source
  FROM commercial_funnel_events registration
  WHERE registration.user_id=NEW.user_id AND registration.event_type='REGISTERED';
  IF acquisition_source IS NOT NULL THEN
    PERFORM record_commercial_funnel_event('API_KEY_CREATED',NEW.user_id,NEW.organization_id,NULL,
      'api_key',NEW.id::text,'funnel:api_key_created:'||NEW.user_id::text,NEW.created_at,
      jsonb_build_object('environment',NEW.environment,'acquisition_source',acquisition_source));
  END IF;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS commercial_funnel_api_key_insert ON api_keys;
CREATE TRIGGER commercial_funnel_api_key_insert
  AFTER INSERT ON api_keys FOR EACH ROW EXECUTE FUNCTION commercial_funnel_api_key_trigger();

CREATE OR REPLACE FUNCTION commercial_funnel_recharge_trigger()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  acquisition_source text;
BEGIN
  SELECT registration.metadata->>'acquisition_source' INTO acquisition_source
  FROM commercial_funnel_events registration
  WHERE registration.user_id=NEW.created_by AND registration.event_type='REGISTERED';
  IF NEW.status IN ('CREDITED','REFUND_PENDING','REFUNDED','CHARGEBACK')
     AND acquisition_source IS NOT NULL
     AND (TG_OP='INSERT' OR OLD.status NOT IN ('CREDITED','REFUND_PENDING','REFUNDED','CHARGEBACK')) THEN
    PERFORM record_commercial_funnel_event('FIRST_RECHARGE',NEW.created_by,NEW.organization_id,NULL,
      'recharge_order',NEW.id::text,'funnel:first_recharge:'||NEW.organization_id::text,
      COALESCE(NEW.credited_at,NEW.paid_at,NEW.updated_at,NEW.created_at),
      jsonb_build_object('currency',NEW.currency,'acquisition_source',acquisition_source));
  END IF;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS commercial_funnel_recharge ON recharge_order;
CREATE TRIGGER commercial_funnel_recharge
  AFTER INSERT OR UPDATE OF status ON recharge_order FOR EACH ROW
  EXECUTE FUNCTION commercial_funnel_recharge_trigger();

CREATE OR REPLACE FUNCTION commercial_funnel_request_log_trigger()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  call_number bigint;
  acquisition_source text;
  funding_complete boolean;
BEGIN
  IF NEW.user_id IS NULL OR NEW.status_code < 200 OR NEW.status_code > 299 THEN
    RETURN NEW;
  END IF;
  SELECT registration.metadata->>'acquisition_source' INTO acquisition_source
  FROM commercial_funnel_events registration
  WHERE registration.user_id=NEW.user_id AND registration.event_type='REGISTERED';
  IF acquisition_source IS NULL THEN
    RETURN NEW;
  END IF;
  IF NEW.funding_operation_id IS NOT NULL THEN
    SELECT funding.status IN ('SETTLED','PARTIALLY_SETTLED','RELEASED') INTO funding_complete
    FROM funding_operation funding WHERE funding.id=NEW.funding_operation_id;
    IF COALESCE(funding_complete,false)=false THEN
      RETURN NEW;
    END IF;
  END IF;
  INSERT INTO commercial_funnel_api_call_counter(
    user_id,successful_call_count,first_request_id,second_request_id,updated_at
  ) VALUES(NEW.user_id,1,NEW.request_id,NULL,now())
  ON CONFLICT(user_id) DO UPDATE SET
    successful_call_count=commercial_funnel_api_call_counter.successful_call_count+1,
    second_request_id=CASE WHEN commercial_funnel_api_call_counter.successful_call_count=1
      THEN EXCLUDED.first_request_id ELSE commercial_funnel_api_call_counter.second_request_id END,
    updated_at=now()
  RETURNING successful_call_count INTO call_number;

  IF call_number=1 THEN
    PERFORM record_commercial_funnel_event('FIRST_API_CALL',NEW.user_id,NEW.organization_id,NULL,
      'request_log',NEW.request_id,'funnel:first_api_call:'||NEW.user_id::text,NEW.created_at,
      jsonb_build_object('endpoint',NEW.endpoint,'acquisition_source',acquisition_source));
  ELSIF call_number=2 THEN
    PERFORM record_commercial_funnel_event('SECOND_API_CALL',NEW.user_id,NEW.organization_id,NULL,
      'request_log',NEW.request_id,'funnel:second_api_call:'||NEW.user_id::text,NEW.created_at,
      jsonb_build_object('endpoint',NEW.endpoint,'acquisition_source',acquisition_source));
  END IF;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS commercial_funnel_request_log_insert ON request_logs;
CREATE TRIGGER commercial_funnel_request_log_insert
  AFTER INSERT ON request_logs FOR EACH ROW EXECUTE FUNCTION commercial_funnel_request_log_trigger();

CREATE OR REPLACE FUNCTION commercial_funnel_subscription_event_trigger()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  acquisition_source text;
BEGIN
  IF NEW.event_type NOT IN ('SUBSCRIPTION_CREATED','PLAN_CHANGED_IMMEDIATELY','PLAN_CHANGE_SCHEDULED')
     OR NEW.actor_id IS NULL THEN
    RETURN NEW;
  END IF;
  SELECT registration.metadata->>'acquisition_source' INTO acquisition_source
  FROM commercial_funnel_events registration
  WHERE registration.user_id=NEW.actor_id AND registration.event_type='REGISTERED';
  IF acquisition_source IS NOT NULL THEN
    PERFORM record_commercial_funnel_event('FIRST_SUBSCRIPTION',NEW.actor_id,NEW.organization_id,NULL,
      'subscription_event',NEW.id::text,'funnel:first_subscription:'||NEW.organization_id::text,
      NEW.created_at,jsonb_build_object('subscription_event_type',NEW.event_type,
        'acquisition_source',acquisition_source));
  END IF;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS commercial_funnel_subscription_event_insert ON subscription_event;
CREATE TRIGGER commercial_funnel_subscription_event_insert
  AFTER INSERT ON subscription_event FOR EACH ROW
  EXECUTE FUNCTION commercial_funnel_subscription_event_trigger();

COMMENT ON TABLE public_commercial_terms IS
  'Versioned public tax, bonus, and refund disclosures. Every row remains marked for lawyer review.';
COMMENT ON TABLE public_payment_fee_schedule IS
  'Versioned exact payment-channel and platform-service fee disclosure; amounts use NUMERIC.';
COMMENT ON TABLE commercial_funnel_events IS
  'Immutable, idempotent milestone evidence. It intentionally excludes prompt/response content and direct identifiers.';
