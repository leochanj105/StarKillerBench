# STATUS.md

Current state of the StarkillerBench Hotel Reservation app — honest about what's a working shakedown stand-in and what's a benchmark-realistic service.

## What's built

Four services are scaffolded as working gRPC services. Each has unit tests that pass and a Dockerfile that builds and answers `/healthz`. All four use **in-memory state and lose all data on restart.** None implements the benchmark-realism behaviors the paper requires.

| service       | RPCs                                     | state            | deps                | status      |
|---------------|------------------------------------------|------------------|---------------------|-------------|
| payment       | Authorize, Capture, Void, Refund         | in-memory map    | (none)              | built ✓     |
| inventory     | SetStock, Hold, Commit, Release          | in-memory map    | (none)              | drafted     |
| booking       | CreateBooking, GetBooking, ListBookings  | in-memory map    | inventory + payment | drafted     |
| cancellation  | Cancel                                   | stateless        | booking + payment   | drafted     |

"Drafted" = `SPEC.yaml` + `steps.yaml` + audited `server_test.go` exist; pipeline not yet run.

## What's been validated

The agent-build pipeline in `framework/`:

- Per-component `SPEC.yaml` → agent generates code inside a bwrap sandbox → checkers verify → pipeline records pass/fail per step.
- Sandbox keeps the agent inside its `scope:` and protects framework files (`.runner/`, `steps.yaml`, `SPEC.yaml`, audited tests).
- Cross-service dependency auto-binding works — `dependent_interface:` in SPEC.yaml drives read-only binds of upstream services' proto + stubs into the sandbox.
- Mock-injected client pattern works for saga-style services (booking, cancellation).

## What's NOT been validated — the benchmark gap

The benchmark target (see `FEATURES.md` and `ARCHITECTURE.md`) requires behaviors that none of the current services have:

| service       | gap from shakedown to benchmark target                                                                     |
|---------------|------------------------------------------------------------------------------------------------------------|
| payment       | + tunable latency / error / timeout distributions (the mock-PSP chaos knob — the systems-research signal) |
| inventory     | + Postgres for durable stock; Redis + Lua for the Hold hot path; TTL'd holds + background sweeper          |
| booking       | + Postgres for durable booking records; NATS event emission (`booking.confirmed`); demand-signal updates   |
| cancellation  | + `inventory.Release` (return stock); refund policy with free-cancel window + penalty schedule             |

Cross-cutting gaps for all services:

- No structured logging, no OTel tracing, no Prometheus metrics. (Observability was explicitly deferred.)
- No chaos profile injection at the framework level.
- No integration tests across services (`booking` saga is only mock-tested).

Until these are added, the benchmark **won't exercise the systems behaviors** the StarkillerBench paper exists to study (write contention on hot keys, async event chains, crash recovery on durable state, observable tail latency).

## Planned iterations (Path A)

The framework is proven; the simplified services are stepping stones. Each iteration revises a service's `SPEC.yaml` + tests and re-runs the pipeline. The framework supports this — re-running on an edited SPEC.yaml re-implements (currently passed steps need their status cleared from `.state.yaml` to force re-run).

1. **payment v2** — add chaos knobs (env-var driven). No new infra. Smallest delta; good next stress test of the prompt template.
2. **inventory v2** — add Postgres + Redis. Big jump: new infra (testcontainers? docker-compose?), new prompts that include DB connection setup, new checkers for DB-level assertions.
3. **booking v2** — add Postgres for durable records + NATS event emission. Async event consumption tests.
4. **cancellation v2** — add `inventory.Release` + refund policy.

After (2) and (3), the benchmark exercises real write contention and real async event chains — the actual systems-research signal.

## Why Path A (shakedown first)

The framework was the unknown — agent-driven build pipelines for microservice benchmarks aren't well-established practice. The benchmark behaviors, by contrast, are well-specified in `FEATURES.md`. Validating that the pipeline works end-to-end (including the booking saga with two upstream services) is the load-bearing methodology contribution. "Make it realistic" is iterative `SPEC.yaml` + test revisions on top of a proven pipeline.
