# (framework) spec.format.md

# `SPEC.yaml` format

Every component has a `SPEC.yaml` describing what the service is. Agents read this as the **single source of truth** for the service's interface and behavior. Checkers parse it to verify the implementation against it. So its structure is fixed.

This document is automatically included in every agent step's context so the agent can interpret SPEC.yaml unambiguously.

## Top-level keys (all required)

| Key | Type | Meaning |
|---|---|---|
| `summary` | string | One paragraph: what the service does and why. |
| `file_organization` | object | Directory layout and module path. Drives scaffolding. |
| `interface` | object | The API this service exposes. |
| `error_semantics` | list | When each RPC returns which error code. |
| `state` | object | Persistence model and data shapes. |
| `dependent_interface` | list | Outbound RPC calls (empty `[]` if leaf). |

## `file_organization`

```yaml
file_organization:
  service_dir: services/payment          # where the service code lives
  go_module: agentbench/services/payment # go module path (used by `go mod init`)
  directories:                            # every directory the service needs
    - services/payment/cmd/payment
    - services/payment/internal/server
    - services/payment/internal/store
    - services/payment/api/v1
    - services/payment/proto/v1           # .proto file lives under the service
    - ...
```

The framework's `scaffold_from_spec` script reads `directories` and `go_module` to create the layout. The framework's `file_organization_matches_spec` checker reads the same keys to verify the layout. Single source of truth for scaffolding.

## `interface`

```yaml
interface:
  protocol: grpc                # grpc | http | nats
  service: PaymentService       # PascalCase service name
  package: payment.v1           # proto package (also used in go_package option)
  rpcs:
    - name: Authorize           # PascalCase RPC name
      request:                  # dict of field → type, or the literal "empty"
        amount: int64
        currency: string
        payment_token: string
      response:                 # dict of field → type, or "empty"
        auth_id: string
      description: |
        One sentence about what this RPC does.
    - name: ...
```

`request` and `response` may each be either a dict of `field: type` or the literal string `empty`. Types are protobuf scalar types: `int32`, `int64`, `uint32`, `uint64`, `string`, `bool`, `bytes`, plus references to other message names in the same package.

## `error_semantics`

```yaml
error_semantics:
  - rpc: Authorize
    condition: amount is non-positive or currency is empty
    code: InvalidArgument
  - rpc: Capture
    condition: unknown auth_id
    code: NotFound
```

One entry per (RPC, condition). `code` is the gRPC status code name (`InvalidArgument`, `NotFound`, `FailedPrecondition`, `Internal`, `Unavailable`, `DeadlineExceeded`, etc.).

Every distinct error path the service intentionally returns must appear here.

## `state`

```yaml
state:
  kind: in_memory               # stateless | in_memory | persistent
  lifetime: per_process         # per_process | persistent | ttl:<seconds>
  stores:
    authorizations:             # name of the store
      kind: map                 # map | table | list | set
      key:
        name: auth_id
        type: string
      value:                    # the value schema, declared inline
        amount: int64
        currency: string
        status: string          # comment for enum-like values
        refunded: int64
```

- `kind` — where state lives.
- `lifetime` — how long it lives.
- `stores` — named storage containers. Each declares its `kind`, its `key`, and its `value` (schema inline).

For `kind: stateless`, omit `stores`.

## `dependent_interface`

```yaml
dependent_interface: [payment, inventory]
```

A bare list of upstream service names. Empty `[]` for leaf services.

Each name `<svc>` in the list implies:

- The agent's Go code can `import "agentbench/services/<svc>/api/v1"`.
- The framework ro-binds `services/<svc>/api/v1`, `services/<svc>/go.mod`, and `services/<svc>/go.sum` into the sandbox.
- The agent must add `replace agentbench/services/<svc> => ../<svc>` to its own `go.mod`.

That's the single auditable declaration of cross-service file access — read this list in a service's `SPEC.yaml` and you know exactly which other services it can import from.


---

# services/booking/SPEC.yaml

summary: |
  booking orchestrates the hotel-reservation saga: hold inventory, authorize
  + capture payment, commit inventory, persist the booking. On any failure
  along the chain it runs compensating actions.

  v2 moves the persisted booking records and idempotency index from process
  memory to Postgres. Bookings survive restarts, and idempotency becomes
  durable and race-safe: two concurrent CreateBooking calls with the same
  idempotency_key never both run the saga. The RPC surface is unchanged.

  The v1 in-memory implementation is retained as `memStore` behind a
  `Store` interface so v1 tests stay frozen and local dev runs without
  Postgres.

file_organization:
  service_dir: services/booking
  go_module: agentbench/services/booking
  directories:
    - services/booking/cmd/booking
    - services/booking/internal/server
    - services/booking/internal/store
    - services/booking/migrations
    - services/booking/api/v1
    - services/booking/proto/v1

interface:
  protocol: grpc
  service: BookingService
  package: booking.v1
  rpcs:
    - name: CreateBooking
      request:
        user_id: string
        hotel_id: string
        room_type: string
        date: string
        amount: int64
        currency: string
        payment_token: string
        idempotency_key: string
      response:
        booking_id: string
      description: |
        Runs the booking saga (inventory.Hold → payment.Authorize →
        payment.Capture → inventory.Commit → persist) and returns the new
        booking_id. If `idempotency_key` was used by a prior successful
        CreateBooking, returns that booking_id without re-running the saga.

    - name: GetBooking
      request:
        booking_id: string
      response:
        user_id: string
        hotel_id: string
        room_type: string
        date: string
        amount: int64
        currency: string
        auth_id: string
        status: string
      description: |
        Returns the persisted booking record. `status` is always "confirmed"
        (failed sagas are not persisted).

    - name: ListBookings
      request:
        user_id: string
      response:
        booking_ids: repeated string
      description: |
        Returns the booking_ids belonging to a user. Empty list if none.

error_semantics:
  - rpc: CreateBooking
    condition: any required string field is empty, or amount is non-positive
    code: InvalidArgument
  - rpc: CreateBooking
    condition: any upstream call in the saga fails (after compensations run)
    code: FailedPrecondition

  - rpc: GetBooking
    condition: unknown booking_id
    code: NotFound

  # ── v2: idempotency contract (locked down by server_v2_test.go) ──────
  # When N concurrent CreateBooking calls carry the same idempotency_key,
  # the saga (inventory.Hold → payment → inventory.Commit) MUST run at
  # most once, and every caller MUST receive the same booking_id. The
  # Postgres backend enforces this with a UNIQUE constraint on the
  # booking record's idempotency_key: the winning insert persists the
  # booking; a losing insert conflicts on the key and the handler returns
  # the already-stored booking_id without re-running the saga.

state:
  # v2 default backend is Postgres; the v1 in-memory backend is kept as an
  # alternate implementation behind the same `Store` interface so the v1
  # test file (server_test.go) continues to pin its behavior.
  backends:
    - name: memStore
      kind: in_memory
      lifetime: per_process
      used_by: ["v1 unit tests (server_test.go)", "local dev when PG_DSN is unset"]
    - name: pgStore
      kind: postgres
      version: ">= 14"
      lifetime: durable
      used_by: ["v2 unit tests (server_v2_test.go)", "production / integration stack"]
      connection_env: PG_DSN
      schema_source: services/booking/migrations/0001_init.sql
      schema_applied_by: external   # not by the service at startup; loaded
                                    # externally (compose init script in
                                    # tests, a migration tool in production)

  # Logical schema: a single bookings store. idempotency_key is a unique
  # field on the booking record (not a separate store) — its uniqueness is
  # what dedups retried/concurrent CreateBooking calls.
  stores:
    bookings:
      key:
        - booking_id: text
      value:
        idempotency_key: text   # unique; dedups retries of CreateBooking
        user_id: text
        hotel_id: text
        room_type: text
        date: text
        amount: int64
        currency: text
        auth_id: text
        status: text            # always "confirmed" in the persisted set

dependent_interface: [inventory, payment]
