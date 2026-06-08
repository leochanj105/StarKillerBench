# STATUS.md

Current state of the StarkillerBench Hotel Reservation app — honest about
what's built, how it diverges from `ARCHITECTURE.md`, and what remains.

Last refreshed: 2026-06-07.

## 1. What's built

**14 of 15 services** are built through the agent pipeline (`framework/`).
Only `frontend` remains. All unit tests, store-backed tests, and the
cross-service integration suite pass.

The reservation/write core and the read/discovery path are both complete; the
four P1 services (ads/review/user/admin) are in. The four original services
have been iterated past their in-memory shakedown to durable/realistic v2+;
the 10 newer services are at v1 shakedown fidelity (in-memory, gRPC-only).

| service       | pri | latest | state                | deps                                      |
|---------------|-----|--------|----------------------|-------------------------------------------|
| payment       | P0  | v2     | in-memory (by design)| (none)                                    |
| inventory     | P0  | v3     | Postgres             | (none)                                    |
| booking       | P0  | v2     | Postgres             | inventory, payment                        |
| cancellation  | P1  | v2     | stateless            | booking, payment, inventory               |
| geo           | P0  | v1     | in-memory            | (none)                                    |
| profile       | P0  | v1     | in-memory            | (none)                                    |
| pricing       | P0  | v1     | in-memory            | (none)                                    |
| search        | P0  | v1     | stateless            | geo, profile, pricing                     |
| auth          | P0  | v1     | in-memory            | (none)                                    |
| notification  | P0  | v1     | stateless            | (none)                                    |
| user          | P1  | v1     | in-memory            | (none)                                    |
| review        | P1  | v1     | in-memory            | (none)                                    |
| ads           | P1  | v1     | in-memory            | (none)                                    |
| admin         | P1  | v1     | stateless            | geo, profile, inventory, pricing, ads     |
| **frontend**  | P0  | —      | **NOT BUILT**        | auth, search, booking, cancellation, user, review |

Realism iterations on the original four are logged in `VERSIONS.md` (payment
chaos+latency; inventory Postgres + no-oversell + ReturnStock; booking
Postgres + race-safe idempotency; cancellation inventory-return). Each newer
service's SPEC summary lists what its v1 defers to a future v2.

The integration suite (`tests/integration/`) brings up all 14 services + 2
Postgres instances via docker-compose and runs: the store-backed unit tests
(inventory + booking), 8 saga scenarios, the read-path aggregation test
(geo+profile+pricing+search), and the admin→search hotelier-to-guest test.
One command: `framework/scripts/run_integration`.

## 2. What's validated — the methodology

The agent-build pipeline is the paper's load-bearing contribution, and it's
proven end-to-end:
- Per-service `SPEC.yaml` → agent generates code inside a bwrap sandbox →
  checkers verify (`go test`, docker `/healthz`) → pipeline records pass/fail.
- Sandbox confines the agent to its `scope:` and protects framework files
  and audited tests (read-only binds).
- Cross-service dependency auto-binding via `dependent_interface:`.
- Mock-injected client pattern for saga services.
- Iteration loop: edit SPEC + add a versioned test file (`server_vN_test.go`)
  → re-run pipeline. Demonstrated across payment, inventory, booking,
  cancellation.
- Scaled to breadth: 10 additional services built v1-from-scratch through
  the same SPEC → audit → generate → verify loop, including fan-out
  aggregators (search, admin) that mock-test their multi-service calls and
  are then verified live in the integration suite.

## 3. Deliberate divergences from ARCHITECTURE.md

`ARCHITECTURE.md` is the original aspirational spec. The build deliberately
took **Path A** — a simplified shakedown first, to de-risk the pipeline
(the unknown) before scaling to the full system. ARCHITECTURE.md was **not**
revised down to match; these are the standing differences, recorded here
rather than in that doc:

1. **Proto layout: `api/v1`, not `contracts/` + `internal/genpb`.** ARCH §4.1
   puts protos in a central `contracts/` dir with stubs in
   `internal/genpb/`. We use per-service `api/v1/`. This was a *fix*, not a
   shortcut: cross-service imports of `internal/...` are forbidden by Go's
   internal-package rule, so generated stubs must live in an importable
   (non-`internal`) package. **`api/v1` supersedes the ARCH convention** —
   ARCH §4.1 is stale on this point.

2. **No shared `pkg/`** (`obs`, `grpcx`, `chaos`, `authx`, `errs`). ARCH §16.4
   forbids reinventing these; we simply haven't built them yet. Services
   currently wire their own minimal main/server. Additive when we want it.

3. **Observability deferred.** No OTel tracing, Prometheus metrics, or
   structured JSON logging (ARCH §8). Services expose `/healthz` only, not
   `/readyz` + `/metrics`. Additive (interceptors) — no rework required.

4. **Chaos is per-service, not a framework.** payment has chaos *tokens*;
   ARCH §10 wants a uniform `CHAOS_PROFILE` interceptor in `pkg/chaos`.
   Reconcilable later.

5. **Error model is plain gRPC status codes**, not ARCH §7's RFC-7807 +
   `google.rpc.ErrorInfo` envelope.

6. **No `/readyz`, no DoD enforcement, no consistency-test suite, no load
   generator** (ARCH §11, §12, §15). The no-overbooking and no-double-charge
   invariants are exercised by integration/unit tests, not a dedicated
   `testing/consistency/` harness.

**None of these is an architectural contradiction.** The structural bones
match ARCH (gRPC on :50051, HTTP on :8080, one Go module per service +
go.work, each service owns its store, distroless images). Closing the gap is
*additive* (more services, layered interceptors), not a restart.

## 4. The remaining gap

Breadth is essentially done (14/15). What remains:

**`frontend` — the last service, deliberately deferred.** It is the HTTP
edge (public JSON/HTML API) that fans out to auth, search, booking,
cancellation, user, and review. It was held back until those dependencies
existed (they now do), and is intentionally left for a future session — the
backend is fully exercisable without it via the gRPC integration suite, so
nothing is blocked. frontend adds reach (a real guest journey + the
"auth on every request" hot path), not new backend capability.

**Fidelity — the cross-cutting layer (§3 items 2–6)** is still unbuilt for
*all* services: observability, the shared `pkg/`, the RFC-7807 error
envelope, a uniform chaos framework, `/readyz` + `/metrics`, a consistency
suite, and a load generator. This is now the dominant remaining work and is
best done as a dedicated pass so it lands uniformly rather than piecemeal.

**Per-service realism** — the 10 newer services are at v1 shakedown fidelity.
Each SPEC summary lists its deferred v2 (geo R-tree/Mongo, profile
cache + batch, pricing demand signal + batch, search availability + ads +
cache, auth Argon2/RS256/Redis, notification NATS worker, user Postgres
optimistic concurrency, review aggregator, ads Postgres/Redis + attribution,
admin auth-scope). The original four already carry their first realism
iteration.

## 5. Plan

Path A continues. Breadth (Phase 1) is complete except frontend.

1. **Breadth** — ✅ done (14/15). `frontend` deferred to a future session.
2. **Cross-cutting fidelity pass** — introduce `pkg/obs`, `pkg/grpcx`,
   `pkg/chaos`, `pkg/errs`; add OTel/metrics/logging interceptors and the
   RFC-7807 envelope uniformly; add `/readyz` + `/metrics`,
   `testing/consistency/`, and a load generator. Likely the highest-leverage
   next phase — it applies to every service and closes most of §3.
3. **Per-service realism iterations** — bring each v1 service to durable
   state + its dominant systems behavior (the deferred-v2 list above), via
   the proven SPEC-edit + versioned-test loop.
4. **NATS event layer** — `booking.confirmed` / `booking.cancelled` /
   `review.posted` / impression streams, wiring notification (v2), pricing
   demand signal, user loyalty accrual, and ads attribution. Pairs with
   booking v3 (event emission).
5. **frontend** — build the edge once the team wants a runnable guest
   journey; revisit alongside or after the fidelity pass so it inherits the
   shared interceptors.

Per-service v1→v2 deferrals are tracked in each service's `SPEC.yaml` summary
and in `VERSIONS.md` as iterations land.
