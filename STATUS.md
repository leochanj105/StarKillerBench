# STATUS.md

Current state of the StarkillerBench Hotel Reservation app — honest about
what's built, how it diverges from `ARCHITECTURE.md`, and what remains.

Last refreshed: 2026-06-07.

## 1. What's built

Four services, each implemented through the agent pipeline (`framework/`),
each iterated past the original in-memory shakedown to a durable/realistic
v2+. All unit tests, store-backed tests, and the cross-service integration
suite pass.

| service       | latest | RPCs                                        | state                     | deps                          |
|---------------|--------|---------------------------------------------|---------------------------|-------------------------------|
| payment       | v2     | Authorize, Capture, Void, Refund            | in-memory (by design)     | (none)                        |
| inventory     | v3     | SetStock, Hold, Commit, Release, ReturnStock| Postgres                  | (none)                        |
| booking       | v2     | CreateBooking, GetBooking, ListBookings     | Postgres                  | inventory, payment            |
| cancellation  | v2     | Cancel                                      | stateless                 | booking, payment, inventory   |

Per-service realism delivered so far (see `VERSIONS.md` for the full log):
- **payment v2** — deterministic chaos tokens (decline / capture-fail /
  refund-fail) + latency injection.
- **inventory v2/v3** — Postgres backing; no-oversell under concurrent Hold
  (row lock); `ReturnStock` (post-commit undo for cancellation).
- **booking v2** — Postgres records; race-safe idempotency (in-process key
  lock + UNIQUE constraint).
- **cancellation v2** — returns the room to inventory on cancel (closes the
  v1 gap).

The cross-service integration suite (`tests/integration/`) brings up the 4
services + 2 Postgres instances via docker-compose and runs 8 saga scenarios
plus the store-backed unit tests. One command: `framework/scripts/run_integration`.

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

Two axes:

**Breadth — 11 of 15 services unbuilt.** Built: inventory, booking, payment,
cancellation. Missing P0: `frontend`, `auth`, `search`, `geo`, `profile`,
`pricing`, `notification`. Missing P1: `ads`, `review`, `user`, `admin`.
This is the dominant remaining work. See §5 below for the build plan.

**Fidelity — the cross-cutting layer (§3 items 2–6)** is unbuilt for *all*
services. Best done as a dedicated pass after breadth, so it's applied
uniformly rather than retrofitted piecemeal.

## 5. Plan

Phase ordering (Path A continued):
1. **Breadth at v1 shakedown fidelity** — build the 11 missing services as
   in-memory, gRPC-only (frontend: HTTP) simplified v1s, in dependency order,
   so the full topology exists. Defer Postgres/Redis/Mongo/NATS to per-service
   v2 iterations, exactly as the original 4 were done.
2. **Per-service realism iterations** — bring new services to durable
   state + their dominant systems behavior (geo R-tree, profile cache,
   pricing demand signal, search result cache, auth Argon2/JWT/Redis, etc.).
3. **Cross-cutting fidelity pass** — introduce `pkg/obs`, `pkg/grpcx`,
   `pkg/chaos`, `pkg/errs`; add OTel/metrics/logging interceptors and the
   RFC-7807 envelope uniformly; add `testing/consistency/` and a load
   generator.
4. **NATS event layer** — `booking.confirmed` / `booking.cancelled` /
   `review.posted` / impression streams, wiring notification, pricing
   demand, user loyalty, and ads attribution.

The detailed v1 build order for Phase 1 is tracked alongside this plan; see
the next section of the working notes / VERSIONS.md as each lands.
