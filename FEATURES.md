# FEATURES.md

Feature catalog for the **Hotel Reservation** application in the **StarkillerBench** suite — an agent-built microservice benchmark suite that extends DeathStarBench with substantially more realistic feature sets, intended to re-evaluate widely-cited microservice research systems (see *StarkillerBench: Improving Microservice Benchmarks with Coding Agents*).

This directory implements one of four StarkillerBench applications (Social Network, **Hotel Reservation**, Movie Reviews, Forum). The real-world counterpart this app mimics is **Booking.com**.

This document is the source of truth for **what** the benchmark implements. `ARCHITECTURE.md` is the source of truth for **how**.

---

## 1. Methodology

StarkillerBench is built using a two-step process (per the paper):

1. **Interrogation.** Enumerate the *complete* feature set of the real-world counterpart, by inspecting the product's public surface, engineering blog posts, partner/extranet documentation, and observable behavior. Because the counterpart is closed-source, an exact copy is impossible; the goal is the full *feature set*, not the implementation.
2. **Implementation.** With coding-agent assistance, build an application that exposes that feature set, mocking only the external dependencies the real product itself depends on (payment processors, ad backends, etc.). Use industry-standard storage (Postgres, Redis) rather than research toys, so the benchmark stresses systems the way a real deployment would.

The paper explicitly identifies four gaps that DSB benchmarks have versus their real counterparts, and which StarkillerBench is built to close: **authentication**, **a real frontend**, **proper consistency checks**, and **support for advertising**. Each of these is treated as a first-class concern in the sections below.

A consequence of the methodology: the benchmark itself is partly a study in agent-built systems. Every service records its build provenance (see `ARCHITECTURE.md` §15.5).

---

## 2. Booking.com Feature Interrogation

The enumerated feature set of the real Booking.com product, grouped. Items marked **IN** are implemented (faithfully or in mocked form) by this benchmark; items marked **OUT** are deliberately excluded with reasons documented in §7.

### Guest-facing
- Browse and search hotels by location, dates, guests, and filters — **IN**
- Map / list views of search results — **IN** (list only; map UI is out)
- Sponsored and organic results mixed in the results page — **IN** (drives `ads` service)
- Hotel detail page with photos, amenities, room types, reviews — **IN** (photos as URLs)
- Real-time per-room availability and pricing — **IN**
- Dynamic pricing (demand, length-of-stay, day-of-week, promo codes) — **IN**
- Room hold while user completes checkout — **IN** (TTL'd holds)
- Multi-step checkout (guest details, payment, confirmation) — **IN**
- Free-cancellation policy with refund computation — **IN**
- Booking modification (date change, room change) — **OUT** (reprice logic adds complexity without new systems signal)
- Bookmarks / wishlists — **OUT**
- Account: register, login, profile, preferences, payment methods — **IN** (real auth; payment methods stubbed)
- Loyalty (Genius-style tiers, points/wallet) — **IN**
- Past trips, upcoming trips views — **IN**
- Reviews: submit after stay, browse, helpful votes — **IN** (helpful votes stubbed)
- Notifications: email, push, SMS — **IN** as async sink (not actually delivered)
- Customer support / chat — **OUT**
- Multi-currency, FX rates — **OUT** (single-currency stub in `pricing`)
- i18n / l10n — **OUT** (English only)
- Fraud screening — **OUT**
- Trust signals (review counts, verified-stay badges) — **IN** (via `review` aggregator)

### Hotel-facing (extranet)
- Property onboarding, room types, photos — **IN** (via `admin`)
- Rate plan management (per room × season) — **IN**
- Inventory / availability management — **IN**
- Promotions / discount codes — **IN**
- Sponsored placement bidding — **IN** (via `ads`)
- Channel manager / PMS integration — **OUT**
- Per-property analytics dashboard — **OUT** (event stream exists, dashboard does not)
- Payouts to hotel — **OUT**

### Cross-cutting
- Search-results ranking blending price, rating, distance, personalization — **IN** (simple weighted blend; personalization stubbed)
- Recommendation widgets ("you may also like") — **IN** (folded into `search` ranking)
- Real-time demand signals feeding pricing — **IN** (via NATS event from `booking`)
- A/B testing infrastructure — **OUT**
- Server-side analytics / event stream — **IN** (NATS topics; sink is stubbed)
- Advertising attribution (impressions → clicks → bookings) — **IN** (event logging)
- Bot protection / rate limiting — **IN** (rate limiting; bot detection stubbed)

---

## 3. Goals and Non-Goals

### Goals
- Reproduce the full *feature shape* of Booking.com per §2, faithful enough that systems behaviors that DSB misses (auth on the hot path, real frontend rendering, consistency-critical inventory, ad serving fan-out) appear in benchmark traces.
- Exercise systems behaviors that DSB does not: real authentication on every request, per-key write contention (inventory), distributed sagas, blocking external dependencies (payment, ad backend), async event pipelines (notifications, attribution), heavier per-request compute (pricing, ranking, ad selection), write-heavy paths (reviews), and admin-side low-volume write traffic.
- Be reproducible: every service runs in a container, every workload run is deterministic given a seed.
- Be small enough that a research team can stand it up and reason about it.

### Non-Goals
- Production-grade fidelity to Booking.com. We mimic the *feature set*, not the business.
- Full geographic, currency, language, or regulatory coverage.
- Real external service integration. Payment processors, ad-bidding backends, email/SMS providers are mocked locally.
- A second language stack — Go-only for the initial build. Revisit only if needed for a specific systems-research scenario.

---

## 4. Service Inventory

Fifteen services. P0 = build first, on the critical path. P1 = build after the core saga works. Each line names the dominant systems behavior the service is *meant* to exercise — that's the filter for what features go inside it.

| #  | Service        | Source                    | Priority | Dominant systems behavior exercised                  |
|----|----------------|---------------------------|----------|------------------------------------------------------|
| 1  | `frontend`     | DSB gap (real UI)         | P0       | HTML + JSON; fan-out origin; render-side latency     |
| 2  | `auth`         | DSB gap (authentication)  | P0       | Crypto on the hot path; session lookup per request   |
| 3  | `search`       | DSB expanded              | P0       | Fan-out aggregator over geo/profile/inventory/pricing/ads |
| 4  | `geo`          | DSB                       | P0       | Spatial index lookups; read-heavy                    |
| 5  | `profile`      | DSB                       | P0       | Read-heavy cache; hot-key skew                       |
| 6  | `pricing`      | NEW (replaces DSB `rate`) | P0       | CPU-heavy compute with hot cache                     |
| 7  | `inventory`    | NEW                       | P0       | Strongly-consistent writes; TTL holds; hot keys      |
| 8  | `booking`      | NEW                       | P0       | Saga orchestration; partial failure handling         |
| 9  | `payment`      | NEW (mock external)       | P0       | Blocking external call; tunable latency/error        |
| 10 | `notification` | NEW                       | P0       | Async event consumer; worker pool                    |
| 11 | `ads`          | DSB gap (advertising)     | P1       | Mock external bidder; impression/click event stream  |
| 12 | `cancellation` | NEW                       | P1       | Reverse saga; compensation                           |
| 13 | `review`       | DSB expanded              | P1       | Write-heavy + batch aggregation                      |
| 14 | `user`         | DSB expanded              | P1       | RMW on wallet/loyalty balance                        |
| 15 | `admin`        | NEW                       | P1       | Low-volume write traffic; second front door          |

Removed from DSB scope (folded into others):
- `rate` → absorbed by `pricing` (richer model)
- `recommendation` → folded into `search`'s ranking step
- `reservation` → split: durable record lives in `booking`; per-night stock lives in `inventory`

Auth and ads are **new services that close DSB gaps explicitly called out by the StarkillerBench paper**. They are not optional: omitting them would reproduce the very simplifications StarkillerBench exists to fix.

---

## 5. Features by Service

Each section lists the features the service must implement. Items marked **[systems-relevant]** are the reason the service exists in the benchmark; everything else is supporting realism.

### 5.1 `frontend` (P0)
- **Real HTML rendering**, server-side via Go `html/template`, for the core user flows: home/search, hotel detail, checkout, confirmation, my-trips, login/register, review submission. **[systems-relevant: render-side latency on the critical path]**
- JSON API also exposed under `/api/v1/*` for the workload generator and for clients that don't render.
- Endpoints: `GET /`, `GET /hotels/{id}`, `POST /search`, `POST /book`, `POST /cancel`, `POST /reviews`, `GET /me`, `GET /trips`, `POST /login`, plus admin endpoints under `/admin/*` (proxied to `admin` service).
- **Auth enforcement**: every request that mutates or reveals user state is validated via `auth.VerifyToken` before fan-out. Public pages skip this. **[systems-relevant: auth on every hot-path request]**
- Static assets served from the same binary (embedded `embed.FS`).
- Request validation, rate limiting (token bucket per IP, backed by Redis).
- Trace context origination; injects `traceparent` and `correlation_id` into all downstream gRPC calls.

### 5.2 `auth` (P0) — **new, closes the DSB authentication gap**
- `Register(email, password)` — real password storage: Argon2id with per-user salt, cost parameters declared in config. **[systems-relevant: CPU cost on the registration path]**
- `Login(email, password)` → access token + refresh token. Real verification, not parsed-not-verified.
- `VerifyToken(token)` → claims. Called by `frontend` and `admin` on essentially every request. Hot path. **[systems-relevant: high-RPS crypto verification]**
- `Refresh(refresh_token)` → new access token.
- `Logout(token)` → revokes session.
- **JWT signing**: RS256 with keys loaded at startup from a mounted secret. Public key endpoint at `/jwks.json` so other services can verify offline if they choose.
- **Session storage**: Redis-backed, keyed by token hash, with TTL. Revocation list is a Redis SET checked on verify.
- **Rate limiting on login**: per-IP and per-email, to model real bot defenses.
- Distinct issuance for **guest** vs **admin** scopes.

### 5.3 `search` (P0)
- `Search(geo box, check_in, check_out, guests, filters)` → ranked list of hotel summaries with price, mixed with sponsored placements. **[systems-relevant: fan-out aggregator]**
- Pipeline per request: call `geo` → call `profile` (batch) → call `inventory.CheckAvailability` (batch) → call `pricing.BatchQuote` (batch) → call `ads.SelectSponsored` (parallel with the above) → blend organic + sponsored → rank.
- Configurable result page size (default 25). Default sponsored ratio: 3 of 25 slots.
- Ranking step: weighted sum of price, rating, distance; light enough to live in-process. **[systems-relevant: per-request compute]**
- Result caching keyed by (geo, dates, guests, filters) with short TTL (5–30s). Sponsored placements bypass the cache because they're auction-driven per impression.
- Emits an `ImpressionLogged` event to NATS per shown hotel (organic + sponsored), consumed by `ads` for attribution and by an analytics sink.

### 5.4 `geo` (P0)
- `Nearby(lat, lng, radius)` → hotel IDs. **[systems-relevant: read-heavy spatial lookup]**
- In-memory R-tree or geohash bucket index, rebuilt at startup from MongoDB.
- No writes at runtime (admin updates trigger a background reload).

### 5.5 `profile` (P0)
- `GetProfile(hotel_id)` and `BatchGetProfiles([hotel_id])`. **[systems-relevant: hot-key cache]**
- Two-tier cache: memcached → MongoDB.
- Returns: name, address, amenities, photos (URLs only), description, rating summary (sourced from `review` aggregator), room types.
- Hot-key skew is the point — workload generator drives Zipfian access; this service is where you'll see cache hit-ratio effects.

### 5.6 `pricing` (P0)
- `Quote(hotel_id, room_type, check_in, check_out, guests)` → per-night prices, total, taxes, fees, currency. **[systems-relevant: CPU-heavy with hot cache]**
- Inputs: base rate, length-of-stay multiplier, day-of-week factor, demand factor, promo code (optional).
- Demand factor: derived from a rolling 24h booking count per hotel, materialized in Redis by `booking` (async via NATS).
- Cache: per (hotel, room_type, date) with short TTL; invalidated when demand signal flips bucket.
- `BatchQuote` for `search` fan-in.

### 5.7 `inventory` (P0) — **the heart of the benchmark; closes the DSB consistency gap**
- Data model: `(hotel_id, room_type, date) → {total, sold, held}` in Postgres. **[systems-relevant: strongly-consistent writes, hot keys]**
- `CheckAvailability(hotel_id, room_type, date_range)` → bool + remaining count. Reads through a Redis cache.
- `Hold(hotel_id, room_type, date_range, quantity)` → `hold_id` with TTL (default 5 minutes). Atomically decrements available stock. Implemented as a Redis Lua script for the hot path, with periodic reconciliation to Postgres. **[systems-relevant: write contention, TTL expiry]**
- `Commit(hold_id)` → converts hold to sold (durable, Postgres transaction).
- `Release(hold_id)` → returns stock (used by compensation).
- Background sweeper: expires holds whose TTL elapsed without commit.
- Overbooking is **not** supported — this is by design, so write contention is visible. Integration tests assert no overbooking under concurrent load (the paper's "proper consistency checks" gap).

### 5.8 `booking` (P0) — saga orchestrator
- `CreateBooking(user_id, hotel_id, room_type, dates, payment_token)` orchestrates: **[systems-relevant: saga, partial-failure handling]**
  1. `inventory.Hold` (sync, gRPC)
  2. `payment.Authorize` (sync, gRPC, blocking external)
  3. `inventory.Commit` (sync)
  4. Persist booking row in Postgres (durable record of truth for bookings)
  5. Emit `BookingConfirmed` event to NATS (consumed by `notification`, `pricing` demand signal updater, `user`/loyalty accrual, `ads` attribution)
- Compensations on failure: `inventory.Release`, `payment.Void`.
- Idempotency: client supplies `idempotency_key`; orchestrator deduplicates.
- `GetBooking(booking_id)`, `ListBookings(user_id)`.

### 5.9 `payment` (P0) — mocked external provider
- `Authorize(amount, currency, token)` → `auth_id`, `Capture(auth_id)`, `Void(auth_id)`, `Refund(auth_id, amount)`. **[systems-relevant: blocking external call]**
- Internally: random latency drawn from a configurable distribution (default: 100ms p50, 800ms p99), configurable error rate (default 1%), configurable timeout rate.
- No persistence required beyond an in-memory map keyed by `auth_id` (resettable per benchmark run).
- Distinct latency/error knobs let researchers study how upstream services behave under bad downstream behavior.
- Per the paper: this is one of the "mocked external services" StarkillerBench applications are expected to include.

### 5.10 `notification` (P0) — async worker
- Consumes from NATS subjects: `booking.confirmed`, `booking.cancelled`, `review.posted`. **[systems-relevant: async worker pool, queue backpressure]**
- Per event, generates 1–3 outbound "messages" (email/sms/push stubs — written to a sink topic, not actually sent).
- Worker pool size configurable; queue depth is a key metric.
- Failure mode: configurable probability that a message handler fails, causing redelivery — exposes retry storms.

### 5.11 `ads` (P1) — **new, closes the DSB advertising gap**
- `SelectSponsored(query, geo, dates, slot_count)` → ranked list of sponsored hotel IDs with bid metadata. Called by `search`. **[systems-relevant: parallel fan-out target with its own latency budget]**
- **Mock external bidder**: an in-process auction over advertiser budgets, modeled after a second-price auction. Per the paper, "advertising backends" are one of the external dependencies StarkillerBench apps mock.
- `LogImpression(hotel_id, slot, query_id)` and `LogClick(hotel_id, query_id)` — fire-and-forget event ingestion, persisted to NATS.
- `SetCampaign(advertiser_id, hotel_id, daily_budget, bid)` — admin-facing.
- Attribution worker: consumes `booking.confirmed`, joins back to recent impressions/clicks for that user, credits the appropriate campaign. **[systems-relevant: stream-join under skew]**
- Budget management in Postgres with a Redis cache of current-day spend per campaign. Spend updates use optimistic concurrency to model real bidder consistency requirements.

### 5.12 `cancellation` (P1) — reverse saga
- `Cancel(booking_id)` → check policy (free-cancel window vs penalty) → call `inventory.Release` (for future-dated bookings) → call `payment.Refund` → mark booking cancelled → emit `BookingCancelled`. **[systems-relevant: reverse saga, conditional compensation]**
- Refund amount computed from a policy table (per hotel: free until X days before check-in, then Y% penalty, then no refund).
- Idempotent on `booking_id`.

### 5.13 `review` (P1)
- `PostReview(booking_id, rating, text, photos)` → write to Postgres (only allowed after stay completed). **[systems-relevant: write-heavy + batch aggregation]**
- `ListReviews(hotel_id, page)` — paginated reads, hot for popular hotels.
- Background aggregator (runs every N seconds): recomputes per-hotel `avg_rating`, `review_count`, `score_breakdown` and pushes into `profile`'s cache layer.
- Photo storage stubbed (URL accepted, no upload).

### 5.14 `user` (P1)
- `GetUser`, `UpdatePreferences`, `ListPaymentMethods` (stubbed), `ListTrips(user_id)`.
- Identity (register/login) is handled by `auth`; this service owns *non-credential* user state.
- Loyalty: `GetPoints(user_id)`, internal `AccrueOnBooking(user_id, amount)` invoked async by NATS consumer. **[systems-relevant: read-modify-write on balance]**
- Points balance uses optimistic concurrency (version column) — concurrent accruals for the same user must serialize.
- Preferences stored in Postgres.

### 5.15 `admin` (P1) — extranet
- Hotel-facing API. Auth: separate admin scope, verified via `auth`. **[systems-relevant: low-volume write-heavy traffic]**
- `UpsertHotel`, `UpsertRoomType`, `SetInventory(hotel_id, room_type, date_range, total)` — writes to `inventory` total counts (without touching `sold`/`held`).
- `SetRatePlan` — base rates per hotel × room_type.
- `SetCampaign` — proxies to `ads`.
- `ListBookings(hotel_id)` — hotel-side view, paginated.
- Distinct front door from `frontend`. Workload generator drives at ~1/100 the rate of guest traffic.

---

## 6. Cross-Cutting Features

Every service implements these. Specified in `ARCHITECTURE.md`; listed here so they appear in service DoD checklists.

- **Structured JSON logging** with trace correlation.
- **OpenTelemetry tracing** on every RPC, DB call, cache call.
- **Prometheus RED metrics** per endpoint.
- **Health and readiness endpoints**.
- **Chaos profile**: every service reads `CHAOS_PROFILE` env var that controls injected latency, error rate, and panic probability. Used by failure-injection workload runs.
- **Idempotency** on all state-mutating RPCs that can be retried (`booking.CreateBooking`, `inventory.Hold`, `payment.Authorize`, `cancellation.Cancel`, `ads.LogImpression`, `auth.Register`).
- **Real consistency checks** (the paper's third DSB gap): every state-mutating RPC has an integration test asserting the consistency invariant it owns. Examples: no overbooking under concurrent holds; no double-charge under retried `Authorize`; no double-accrual of loyalty points under duplicate event delivery; no negative ad budget under concurrent spend.
- **Graceful shutdown** on SIGTERM — drains in-flight RPCs, closes DB pools.

---

## 7. Workload Generator Features

Not a service, but a first-class deliverable. Lives in `workload/`.

- **Traffic mix** (configurable; defaults reflect Booking-like ratios):
  - 65% search (organic + sponsored impressions logged)
  - 18% hotel-detail page view (profile + pricing for one hotel)
  - 5% sponsored click-through
  - 5% start booking (search → book attempt, may abandon mid-saga)
  - 3% complete booking
  - 1% cancellation
  - 0.5% review submission
  - 0.5% admin write
  - 2% login / register / token refresh (driven by session model below)
- **Popularity skew**: Zipfian over hotels (default α=1.0), so the top hotels dominate reads and contend on writes.
- **Date skew**: weekend and holiday peaks; bookings cluster on a small subset of date ranges.
- **Session model**: users perform plausible sequences (login → search → click → maybe book), not independent requests, so trace fan-out and per-request auth cost are realistic.
- **Ad attribution closure**: a configurable fraction of sponsored clicks result in a downstream booking, so the `ads` attribution path is exercised end-to-end.
- **Failure injection**: drive `CHAOS_PROFILE` on selected services mid-run; record SLO impact.
- **Determinism**: seedable; same seed + config → same trace.
- **Output**: per-run summary (RPS, latency percentiles per endpoint, error rate, queue depths) plus raw histograms exported to Prometheus.

---

## 8. Explicitly Out of Scope

Each of these is a real Booking.com feature; each is excluded because it adds code without changing the systems characteristics the benchmark is meant to expose. Listed so future contributors don't waste agent-cycles on them.

| Feature                  | Why excluded                                                       |
|--------------------------|--------------------------------------------------------------------|
| Real payment / 3DS       | `payment` is mocked per StarkillerBench convention                 |
| Real ad-bidding backend  | `ads` is mocked per StarkillerBench convention                     |
| Fraud detection          | Pure rule-engine work; doesn't change call-graph shape             |
| Customer support / chat  | Separate stack; out of the reservation hot path                    |
| Multi-currency FX rates  | Stubbed in `pricing`; live FX adds an external call we don't need  |
| Photo upload / CDN       | URLs only; binary handling is its own benchmark                    |
| Full i18n / l10n         | Strings are English only; translation adds code, not systems load  |
| Channel manager bridging | PMS integration is its own world; not in scope                     |
| Booking modification     | Re-price logic adds complexity without a new systems signal        |
| A/B testing harness      | Decoupled from request hot path; orthogonal to the benchmark       |
| Email/SMS/push delivery  | `notification` writes to a sink topic; no real delivery            |
| Hotelier analytics UI    | Event stream exists; no dashboard rendering                        |

Note: **authentication and advertising are explicitly *in scope*** because they are gaps the StarkillerBench paper identifies as material differences from real systems.

---

## 9. Mapping to StarkillerBench Re-Evaluation Targets

The StarkillerBench paper re-evaluates 3–5 widely-cited research systems across four categories of microservice research. For each category, the features below are the ones that produce the relevant signal. This is the answer to "why these features?" for paper reviewers.

### 9.1 Communication between microservices
Systems studied: RPC frameworks, service meshes, proxyless gRPC variants, alternative serialization.
- `search` fan-out (5+ parallel callees including `ads`) gives realistic request-size distribution and concurrent connection patterns.
- `frontend` → `auth.VerifyToken` on every authenticated request gives a high-RPS, tiny-payload RPC characteristic of mesh-amplified call counts.
- `booking` saga generates sequential cross-service chains that meshes typically struggle with.

### 9.2 Microservice orchestrators / schedulers
Systems studied: SigmaOS, μs-scale schedulers, autoscalers, FaaS placement.
- `inventory.Hold` is the hot, small, contended write — exactly the workload microsecond-scale schedulers target.
- `pricing` and `ads` are CPU-heavy services with bursty load tied to `search` fan-out; good autoscaling targets.
- Asymmetric guest (`frontend`) vs admin (`admin`) traffic provides realistic priority-scheduling scenarios.

### 9.3 Microservice debugging / observability tools
Systems studied: distributed tracing analyzers, anomaly detection, root-cause attribution.
- Booking saga produces complex, multi-level traces with conditional compensation branches — non-trivial RCA targets.
- Async fan-out via NATS (`booking.confirmed` → notification + loyalty + pricing demand signal + ads attribution) produces causally-related traces that don't share a parent span.
- `CHAOS_PROFILE` lets researchers inject specific failure modes to test detection accuracy.

### 9.4 Microservice frameworks
Systems studied: serverless / FaaS platforms, polyglot frameworks, service-mesh control planes.
- Mix of stateful (`inventory`, `booking`, `auth`, `ads`) and stateless (`search`, `cancellation`) services tests framework cold-start, scaling, and packaging assumptions.
- Real authentication on the hot path means frameworks must support shared crypto state or accept the RPC cost — a real architectural trade-off.
- The `ads` attribution worker is a long-running stream consumer; frameworks targeting only synchronous-RPC workloads will not cover it.
