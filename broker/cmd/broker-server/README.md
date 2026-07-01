# broker-server

Broker HTTP service for the local AI compute broker.

Current implementation:

- load config
- initialize storage, cache, and policy components
- expose the broker HTTP API
- submit and track backend jobs
- enforce authn, authz, and result-release policy
- write tamper-evident audit events

Authentication modes:

- `BROKER_AUTH_MODE=header`
  - trusts `X-Broker-Actor` and optional `X-Broker-Role`
  - suitable only behind an authenticated internal gateway
- `BROKER_AUTH_MODE=static_tokens`
  - requires `Authorization: Bearer <token>`
  - tokens are configured with `BROKER_STATIC_TOKENS`
  - format: `token1=alice:user,token2=ops:admin`

Audit logging:

- `BROKER_AUDIT_LOG_PATH`
  - append-only JSONL audit sink
  - default: `.broker/audit.jsonl`
  - records are hash-chained with `prev_hash` and `event_hash`
- `BROKER_AUDIT_VERIFY_MODE`
  - startup integrity policy for the active audit chain
  - values: `fail`, `warn`, `off`
  - default: `fail`
- `BROKER_AUDIT_ROTATE_BYTES`
  - rotate the active audit file when it reaches this size
  - default: `10485760` (10 MiB)
- `BROKER_AUDIT_KEEP_ARCHIVES`
  - number of rotated audit segments to retain
  - default: `10`
- `BROKER_AUDIT_MAINTAIN_INTERVAL_SECONDS`
  - background maintenance interval for rotate/prune checks
  - default: `300`

Audit integrity behavior:

- on startup, the service verifies the active audit chain according to `BROKER_AUDIT_VERIFY_MODE`
- at runtime, admins can check current audit integrity with `GET /v1/system/audit-health`
- the audit health endpoint:
  - requires an admin principal
  - returns `200` when the active chain validates
  - returns `503` when the chain is present but invalid
  - returns `501` when audit logging is not configured

Current HTTP endpoints:

- `GET /healthz`
- `POST /v1/jobs`
- `GET /v1/jobs`
- `GET /v1/jobs/{id}`
- `GET /v1/jobs/{id}/result`
- `GET /v1/jobs/{id}/logs`
- `POST /v1/jobs/{id}:cancel`
- `GET /v1/system/audit-health`
