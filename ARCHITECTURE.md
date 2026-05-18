# ARCHITECTURE.md

The single source of truth for **how** services in the StarkillerBench Hotel Reservation application are built. Every service must conform to this document. Deviations require updating this file first.

This directory implements one of four StarkillerBench applications — the **Hotel Reservation** app, modeled after Booking.com. See *StarkillerBench: Improving Microservice Benchmarks with Coding Agents* for the methodology. Sibling apps (Social Network, Movie Reviews, Forum) live in separate directories/repos; this document governs only the Hotel Reservation app.

`FEATURES.md` defines *what* is built (including the Booking.com feature interrogation). This document defines *how*.

---

## 1. Overview

```
                            ┌──────────────┐         ┌──────────────┐
   guests ────────────────► │   frontend   │         │    admin     │ ◄─── hoteliers
   (HTML + JSON)            │ (HTML+JSON)  │         │ (HTTP/JSON)  │
                            └──────┬───────┘         └──────┬───────┘
                                   │                        │
                       VerifyToken │                        │ VerifyToken
                                   ▼                        ▼
                            ┌──────────────┐         ┌──────────────┐
                            │     auth     │ ◄───────┤              │
                            └──────────────┘         └──────────────┘
                                   │
            ┌──────────────────────┴──────────────────────┐
            ▼                                             ▼
       ┌────────┐                                   ┌──────────┐  saga    ┌──────────┐
       │ search │                                   │ booking  │─────────►│ payment  │
       └───┬────┘                                   └────┬─────┘          └──────────┘
           │ fan-out                                     │
   ┌───────┼────────┬──────┬────────┐                    │
   ▼       ▼        ▼      ▼        ▼                    ▼
 ┌────┐ ┌──────┐ ┌────┐ ┌─────────┐ ┌─────────┐    ┌──────────────┐
 │geo │ │profile│ │ads │ │pricing  │ │inventory│    │ cancellation │
 └────┘ └──────┘ └─┬──┘ └─────────┘ └─────────┘    └──────────────┘
                  │
                  │ ┌─────────┐  ┌──────┐  ┌──────────────┐
                  │ │ review  │  │ user │  │ notification │
                  │ └─────────┘  └──────┘  └──────────────┘
                  │                              ▲
                  └────────── NATS JetStream ────┘
                  (booking.confirmed, booking.cancelled, review.posted,
                   ad.impression, ad.click, inventory.expired)
```

All synchronous calls are gRPC. All cross-service async is NATS JetStream. Each service owns its data store; no cross-service direct DB access. `auth` is on the hot path for any authenticated request — its latency is part of the user-perceived critical path, per the paper's "DSB authentication gap".

---

## 2. Repository Layout

This directory (the Hotel Reservation app) is laid out as a single monorepo. Sibling StarkillerBench apps may sit under a parent directory or as separate repos — that's a suite-level decision, not a per-app one.

```
agentbench/                         # this directory; the Hotel Reservation app
├── ARCHITECTURE.md                 # this file
├── FEATURES.md                     # feature catalog + Booking.com interrogation
├── Makefile                        # top-level: all, integration-test, load-test, lint
├── go.work                         # Go workspace tying services together
│
├── contracts/                      # all .proto files; the canonical service interfaces
│   ├── auth/v1/auth.proto
│   ├── geo/v1/geo.proto
│   ├── profile/v1/profile.proto
│   ├── inventory/v1/inventory.proto
│   ├── ads/v1/ads.proto
│   ├── ...
│   └── events/v1/events.proto      # CloudEvents-typed payload schemas for NATS
│
├── pkg/                            # shared Go libraries (small, stable)
│   ├── obs/                        # logging + tracing + metrics setup
│   ├── grpcx/                      # interceptors: tracing, logging, metrics, recovery
│   ├── chaos/                      # CHAOS_PROFILE parser + injectors
│   ├── ids/                        # ULID / UUID helpers
│   ├── authx/                      # JWT verification client for service-side auth checks
│   └── errs/                       # error envelope + retryable helpers
│
├── services/
│   ├── profile/                    # ← reference service. Imitate this layout.
│   │   ├── cmd/profile/main.go
│   │   ├── internal/
│   │   │   ├── server/             # gRPC handlers
│   │   │   ├── store/              # data access (mongo + memcached)
│   │   │   └── config/
│   │   ├── Dockerfile
│   │   ├── Makefile
│   │   ├── README.md
│   │   └── test/
│   │       └── integration/
│   ├── auth/
│   ├── inventory/
│   ├── booking/
│   ├── ads/
│   ├── frontend/                   # also contains templates/ and static/
│   └── ...
│
├── deploy/
│   ├── docker-compose.yml          # local dev; one-command bring-up
│   └── k8s/                        # benchmark-run manifests
│
├── workload/                       # load generator
│   ├── cmd/wgen/main.go
│   ├── internal/
│   │   ├── mix/                    # traffic mix
│   │   ├── popularity/             # Zipfian, date skew
│   │   └── sessions/               # session model with login flow
│   └── README.md
│
├── testing/
│   ├── integration/                # cross-service tests; depend on docker-compose
│   ├── consistency/                # invariant tests: no overbooking, no double-charge, etc.
│   ├── fixtures/                   # seed data generators
│   └── trace-asserts/              # helpers for asserting on Jaeger/OTel traces
│
└── docs/
    ├── interrogation/              # Booking.com feature interrogation evidence
    │   └── booking-com.md
    ├── agent-prompts/              # prompt templates for building each service
    │   └── service-template.md
    └── adr/                        # architecture decision records, numbered
```

Service directories are **uniform**. Every service has the same `cmd/`, `internal/{server,store,config}`, `Dockerfile`, `Makefile`, `README.md`, `test/integration/` layout. The `frontend` service additionally contains `templates/` and `static/` directories. Agents must not invent variations.

---

## 3. Language and Runtime

- **Go 1.22+** for all services and the workload generator. No second language in the initial build.
- **Modules**: one Go module per service, all wired via top-level `go.work`. Shared code lives under `pkg/` as its own module.
- **Container base image**: `gcr.io/distroless/static-debian12` for service binaries. Build stage: `golang:1.22-bookworm`.
- **Linters**: `golangci-lint` with the repo-pinned config; `make lint` runs it. Lint must be clean before DoD passes.
- **Formatting**: `gofmt -s` and `goimports`. Enforced in CI.

---

## 4. Inter-Service Communication

### 4.1 Synchronous: gRPC
- All service-to-service calls are gRPC over HTTP/2 on port `:50051`.
- Protos live in `contracts/<service>/v1/`. Generated stubs are committed at `services/<service>/internal/genpb/` (don't regenerate in CI for stability).
- Every gRPC server registers these interceptors, in this order:
  1. Recovery (panic → `Internal` status)
  2. Tracing (extract `traceparent` from metadata)
  3. Logging (request/response logging at debug)
  4. Metrics (RED histogram)
  5. Chaos (inject latency/error per `CHAOS_PROFILE`)
- Clients use the matching client-side interceptor chain plus:
  - **Timeouts**: every outbound RPC has an explicit per-call deadline. No infinite-wait calls. Default 2s for read RPCs, 5s for writes; tunable per call site.
  - **Retries**: only on `Unavailable` and `DeadlineExceeded`, max 2 retries, exponential backoff (50ms, 200ms). Retries are off for non-idempotent calls.

### 4.2 Public HTTP
- `frontend` and `admin` expose HTTP. `frontend` serves both HTML (server-side rendered with `html/template`, mounted under `/`) and JSON (under `/api/v1/`). `admin` is JSON-only.
- JSON request/response bodies follow standard JSON conventions (camelCase fields).
- HTML responses use server-side templates with CSRF tokens on form submissions.
- Errors (JSON path) use **RFC 7807 Problem Details**:
  ```json
  { "type": "about:blank", "title": "...", "status": 400, "detail": "...",
    "correlation_id": "01HX...", "retryable": false }
  ```
- Errors (HTML path) render an error template; the same correlation_id is exposed in the page for support.

### 4.3 Authentication on the hot path
- Authenticated requests carry a bearer JWT (access token).
- `frontend` and `admin` call `auth.VerifyToken` on **every** request that requires authentication. Caching of verification results is **not** permitted in v1 — the cost of crypto on the hot path is exactly the systems behavior the benchmark is meant to expose. Researchers studying auth-caching mechanisms can introduce caching as part of their evaluated system.
- Services downstream of `frontend` trust the verified user ID propagated via gRPC metadata under `x-user-id`, `x-user-scope`. The propagating service is responsible for setting these only after verification succeeds.

### 4.4 Asynchronous: NATS JetStream
- Single NATS cluster; subjects namespaced by domain:
  - `booking.confirmed`, `booking.cancelled`
  - `review.posted`
  - `inventory.expired`
  - `ad.impression`, `ad.click`
- Payload format: **CloudEvents v1.0** JSON. Schemas in `contracts/events/v1/`.
- Each consumer is a durable JetStream consumer with explicit ack and a configurable max-deliver.
- Producers do not call consumers directly; only emit events. Consumers are listed in each producer's README under "Downstream subscribers" (informational).

---

## 5. Data Stores

Each service owns one or more stores. **No cross-service direct DB access** — if service A needs data from service B, it calls B's RPC. Storage choices follow the StarkillerBench convention (per the paper): industry-standard systems, not research toys.

| Service       | Primary store        | Secondary           | Notes                                      |
|---------------|----------------------|---------------------|---------------------------------------------|
| `auth`        | Postgres (users)     | Redis (sessions, JWKS cache) | Argon2id password hashes; JWT signing keys mounted |
| `profile`     | MongoDB              | memcached           | DSB-inherited; hot-key cache                |
| `geo`         | MongoDB (load-time)  | in-memory R-tree    | Rebuilt at startup                          |
| `pricing`     | Postgres (rate plans)| Redis (price cache, demand signal) |                              |
| `inventory`   | Postgres             | Redis (holds + cache)| Hot path is Redis Lua script               |
| `booking`     | Postgres             | —                   | Source of truth for bookings                |
| `payment`     | in-memory            | —                   | Reset per benchmark run                     |
| `ads`         | Postgres (campaigns) | Redis (current-day spend) | Optimistic concurrency on spend       |
| `cancellation`| (uses booking + payment via RPC)| —      | Stateless                                   |
| `notification`| in-memory            | Redis (dedup)       | Worker state only                           |
| `review`      | Postgres             | memcached           | Aggregator updates `profile` indirectly     |
| `user`        | Postgres             | Redis (cache)       | Wallet uses optimistic concurrency          |
| `admin`       | (uses inventory + pricing + profile + ads via RPC) | — |                              |
| `search`      | (none — pure aggregator)| Redis (result cache) |                                          |
| `frontend`    | (none)               | Redis (rate limit, CSRF) |                                        |

DB connections come from pooled clients configured via env. Connection limits per service: `DB_MAX_CONNS=25` default; tunable.

---

## 6. Service Skeleton

Every service exposes:

- **gRPC** on `:50051` — its domain RPCs.
- **HTTP admin** on `:8080`:
  - `GET /healthz` → 200 if process alive.
  - `GET /readyz` → 200 if dependencies reachable; 503 otherwise.
  - `GET /metrics` → Prometheus exposition format.
- **Signals**: SIGTERM triggers graceful shutdown — stop accepting new RPCs, wait up to 10s for in-flight to complete, close pools.
- **Startup order**: load config → init observability → init store clients → start gRPC + HTTP servers → register readyz=true.

`frontend` additionally exposes its public HTTP listener on a third port (`PUBLIC_PORT`, default `:80`).

---

## 7. Error Model

Errors travel on three surfaces (gRPC, HTTP, events) but share one envelope:

```
code:           machine-readable string, lowerCamelCase   e.g. "inventoryHoldConflict"
message:        human-readable, no PII                    e.g. "no stock for requested dates"
retryable:      bool                                       per the table below
correlation_id: ULID                                       same as trace_id where available
```

| Surface | How it's encoded                                                              |
|---------|--------------------------------------------------------------------------------|
| gRPC    | `status.Status` with the standard code + `google.rpc.ErrorInfo` detail carrying our envelope |
| HTTP    | RFC 7807 Problem Details JSON                                                  |
| Event   | A `*.failed` event with the envelope as the CloudEvents `data` field          |

**Retryable** semantics (binding rule for clients):
- `retryable=true` only for `Unavailable`, `DeadlineExceeded`, `ResourceExhausted` (without quota signal).
- Never for `InvalidArgument`, `AlreadyExists`, `FailedPrecondition`, `PermissionDenied`, `Unauthenticated`.

---

## 8. Observability

### 8.1 Logging
- **Format**: JSON to stdout, one event per line.
- **Required fields**: `ts` (RFC3339 nano), `level` (`debug|info|warn|error`), `service`, `trace_id`, `span_id`, `msg`.
- **No PII** in logs. No raw card numbers, full names, or emails. If a field is needed for debugging, hash it.
- Implemented via `pkg/obs`; do not pull in additional logging libraries.

### 8.2 Tracing
- **OpenTelemetry SDK** with OTLP gRPC exporter pointed at the cluster's OTel Collector.
- **Spans**: one per inbound RPC, one per outbound RPC, one per DB call, one per cache call, one per outbound HTTP call. Background workers create one span per processed event.
- **Context propagation**: W3C `traceparent` on HTTP; `traceparent` in gRPC metadata. Always propagate; never drop the parent.
- **Sampling**: parent-based; root sampler at 100% during benchmark runs (configurable via `OTEL_TRACES_SAMPLER_ARG`).

### 8.3 Metrics
Every gRPC server emits:
- `rpc_server_requests_total{service,method,code}` — counter
- `rpc_server_duration_seconds{service,method,code}` — histogram (buckets: 1ms…30s exponential)
- `rpc_server_in_flight{service,method}` — gauge

Every gRPC client emits:
- `rpc_client_requests_total{caller,service,method,code}`
- `rpc_client_duration_seconds{caller,service,method,code}`

Every service additionally exports its own metrics; declared in its README.

---

## 9. Configuration

- **12-factor**: env vars are authoritative. An optional `/etc/svc/config.yaml` overlays env, not the other way around.
- **Required env vars on every service**:
  - `SERVICE_NAME` — e.g. `inventory`
  - `LOG_LEVEL` — `debug|info|warn|error` (default `info`)
  - `GRPC_PORT` (default `50051`), `HTTP_PORT` (default `8080`)
  - `OTEL_EXPORTER_OTLP_ENDPOINT`
  - `CHAOS_PROFILE` — see §10
- **Per-service env vars**: `DB_DSN`, `REDIS_URL`, `MONGO_URL`, `NATS_URL`, etc. Declared in each service README's "Configuration" section.
- **Auth-specific**: `auth` reads `JWT_PRIVATE_KEY_PATH`, `JWT_PUBLIC_KEY_PATH`, `JWT_ISSUER`, `JWT_ACCESS_TTL`, `JWT_REFRESH_TTL`, `ARGON2_TIME`, `ARGON2_MEMORY`, `ARGON2_PARALLELISM`. Other services use `pkg/authx` which reads `AUTH_JWKS_URL`.
- **No secrets in code, no secrets in committed configs.** For local dev, `deploy/docker-compose.yml` sets dummy values and generates a dev RSA keypair on container start.

---

## 10. Chaos / Fault Injection

Every service reads `CHAOS_PROFILE`, a JSON env var (empty = disabled):

```json
{
  "latency": { "p50_ms": 0, "p99_ms": 0 },
  "errors":  { "rate": 0.0, "code": "Unavailable" },
  "panics":  { "rate": 0.0 }
}
```

Implemented once in `pkg/chaos` and installed as a server interceptor. Applies only to inbound RPCs (not internal background tasks). The workload generator can flip these mid-run to study failure response.

---

## 11. Consistency Invariants and Verification

Per the StarkillerBench paper's "proper consistency checks" gap, every state-mutating RPC owns a documented invariant and an automated test in `testing/consistency/`.

| Invariant                                                | Owning service | Test                                       |
|----------------------------------------------------------|----------------|--------------------------------------------|
| No overbooking under concurrent holds                    | `inventory`    | `consistency/no_overbooking_test.go`       |
| No double-charge under retried Authorize                 | `payment` + `booking` | `consistency/no_double_charge_test.go` |
| No double-accrual of loyalty points under duplicate event| `user`         | `consistency/no_double_accrual_test.go`    |
| No negative ad budget under concurrent spend             | `ads`          | `consistency/no_negative_budget_test.go`   |
| No partial booking under saga abort                      | `booking`      | `consistency/saga_atomicity_test.go`       |

These tests run under concurrent load with chaos enabled. They are part of `make integration-test` and must pass before any service is "done".

---

## 12. Build, Test, Lint

Every service exposes the same Make targets:

```
make build           # compiles binary into bin/
make test            # runs go test ./...
make lint            # runs golangci-lint
make image           # builds Docker image, tagged starkiller/hotel-<svc>:dev
make integration     # runs tests in test/integration against a docker-compose env
```

Top-level Makefile:

```
make all             # build + lint + test for every service
make up              # docker-compose up -d
make down            # docker-compose down -v
make integration-test# spins up env, runs cross-service tests in testing/integration
make consistency-test# spins up env, runs invariant tests in testing/consistency under load
make load-test       # runs workload generator at default profile
```

CI (GitHub Actions): runs `make all`, `make integration-test`, and `make consistency-test` on every PR.

---

## 13. Deployment

- **Local dev**: `make up` brings up the full system via `deploy/docker-compose.yml`. Includes Postgres, Redis, MongoDB, memcached, NATS, OTel Collector, Jaeger UI, Prometheus, Grafana. JWT keypair is generated on first start.
- **Benchmark runs**: Kubernetes manifests in `deploy/k8s/`. One Deployment per service, one StatefulSet per stateful store, NetworkPolicy isolating cross-service DB access.
- **Resource requests**: every Deployment declares CPU + memory requests/limits — these are part of the benchmark contract (researchers need to know what they're scheduling against).

---

## 14. Versioning and Compatibility

- All protos live under `contracts/<service>/v1/`. Bump to `v2` for breaking changes.
- Pre-1.0 of the benchmark itself: no backwards-compat shims. If a contract changes, update all callers in the same change. **Do not** add deprecated fields or migration code "just in case".
- Tag the benchmark repo with semver. A research paper citing the benchmark should pin a tag.

---

## 15. Definition of Done (per service)

A service is "done" when every box below is true. Agent prompts must include this checklist verbatim.

1. **Contract conformance**: implements every RPC declared in `contracts/<service>/v1/<service>.proto`; field names, types, and semantics match.
2. **Layout**: matches the reference service (`services/profile/`) directory structure.
3. **Build**: `make build` succeeds inside the service directory.
4. **Lint**: `make lint` clean (no warnings, no nolint pragmas added without comment).
5. **Tests**: `make test` passes; unit coverage ≥ 60% on `internal/`.
6. **Integration**: a test exists in `testing/integration/` covering the service's happy path against a real (docker-compose) dependency.
7. **Consistency**: if the service owns an invariant in §11, the corresponding test exists in `testing/consistency/` and passes under concurrent load.
8. **Image**: `make image` produces a runnable container; `docker run` followed by a curl to `/healthz` returns 200.
9. **Observability**: emits the required logs, traces, and metrics per §8. Verified by an integration test that asserts the trace contains the expected spans.
10. **Error model**: returns errors per §7; integration test asserts at least one negative case returns the proper code + envelope.
11. **Auth**: if the service is reachable from `frontend`/`admin` and requires auth, it reads `x-user-id` / `x-user-scope` from gRPC metadata and rejects calls without them. Integration test covers a rejected call.
12. **Config**: documents every env var it reads in its `README.md` under "Configuration".
13. **Chaos**: respects `CHAOS_PROFILE`; an integration test confirms latency injection works.
14. **Idempotency**: state-mutating RPCs declared idempotent in `FEATURES.md` deduplicate on the idempotency key (integration test required).
15. **No TODO/FIXME** in committed code. `make lint` enforces this via a custom checker.
16. **README** is complete: purpose, RPC list, dependencies (services + stores), env vars, exposed metrics, known limitations.
17. **Provenance**: README ends with a Provenance section per §16.5.

---

## 16. Agent Workflow (Claude Code)

This benchmark is built by Claude Code instances, one per service (largely in parallel). The conventions below are mandatory; their purpose is to keep parallel agents from drifting. The agent-built methodology is itself a contribution of the StarkillerBench paper.

### 16.1 Standard service-build prompt
Each service has a prompt brief in `docs/agent-prompts/<service>.md`, derived from `docs/agent-prompts/service-template.md`. The brief always includes:

- Link to `ARCHITECTURE.md` (this file)
- Link to `FEATURES.md` and specifically the service's `§5.X` subsection
- Link to `docs/interrogation/booking-com.md` for the real-world feature evidence behind that section
- Link to `contracts/<service>/v1/<service>.proto`
- Link to the reference service `services/profile/` — "match this layout and conventions"
- The DoD checklist (§15) copied inline
- An explicit "stop and ask" list: when proto needs changing, when adding a new dependency, when introducing a new shared package

### 16.2 Required loop
1. **Plan mode first.** The agent enters Plan mode and produces a plan that maps each DoD item to concrete files it will create or modify. Human reviews and accepts the plan before code is written.
2. **Reference service first.** Before writing any code, the agent reads `services/profile/` end-to-end. The prompt enforces this.
3. **Implement → verify locally.** `make build && make lint && make test`.
4. **Integration verify.** `make up` then `make integration` for this service.
5. **Consistency verify** (if applicable). `make consistency-test` for the relevant invariant.
6. **Trace verify.** Open Jaeger UI; confirm spans for at least one happy-path call. (An integration test should do this automatically; this is a backstop.)

### 16.3 Sub-agent usage
- Spawn a sub-agent for **test writing** once the main implementation compiles. Tests-as-second-pass tends to catch real bugs rather than rubber-stamping the implementation.
- Spawn a sub-agent for **README writing** once tests pass. Keeps the implementation agent focused.
- Do **not** spawn sub-agents for Dockerfile or Makefile — copy them from the reference service and adapt.

### 16.4 Conventions agents must not invent
- No new logging library. Use `pkg/obs`.
- No new error type system. Use `pkg/errs`.
- No new retry/backoff helper. Use `pkg/grpcx`.
- No new auth verification client. Use `pkg/authx`.
- No new config loader. Use the convention from the reference service.
- No new build system. Use the per-service Makefile.

### 16.5 Provenance
Every service's `README.md` ends with a "Provenance" section: which Claude model produced it, which prompt template, the date, and approximate human-hours of supervision. This is a methodology deliverable for the StarkillerBench paper, which reports per-application human-hours as evidence that agent-built benchmarks are tractable.

---

## 17. Open Decisions (TBD)

Items deliberately deferred. Each gets a numbered ADR in `docs/adr/` once decided.

- **ADR-001**: Postgres flavor (vanilla PG vs Citus for `inventory` sharding study).
- **ADR-002**: NATS vs Redpanda for the async transport. Default NATS; revisit if researchers need stronger ordering guarantees.
- **ADR-003**: Whether to add a polyglot service in Phase 2 (candidate: rewrite `pricing` in Python for a CPU-bound contrast).
- **ADR-004**: Whether `frontend` should also speak GraphQL for a different fan-out shape.
- **ADR-005**: Whether `auth` verification should be allowed to cache per-token (currently forbidden in v1; researchers studying auth-cache designs may want this).
- **ADR-006**: How sibling StarkillerBench apps (Social Network, Movie Reviews, Forum) share `pkg/` — vendored, git submodule, or duplicated.

Do not act on these until the ADR is written and accepted.
