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

# services/inventory/SPEC.yaml

summary: |
  inventory tracks per-(hotel, room_type, date) room stock for the booking
  saga. An admin populates totals via SetStock; bookings reserve units via
  Hold and then either Commit (confirm the sale) or Release (cancel before
  confirm).

  v2 moves state from process memory to Postgres. Stock and holds survive
  restarts, and concurrent Holds against the same key serialize correctly
  via row-level locks (no oversell). The RPC surface is unchanged from v1.

  The v1 in-memory implementation is retained as `memStore` behind a
  `Store` interface so v1 tests stay frozen and local dev can run without
  Postgres.

file_organization:
  service_dir: services/inventory
  go_module: agentbench/services/inventory
  directories:
    - services/inventory/cmd/inventory
    - services/inventory/internal/server
    - services/inventory/internal/store
    - services/inventory/migrations
    - services/inventory/api/v1
    - services/inventory/proto/v1

interface:
  protocol: grpc
  service: InventoryService
  package: inventory.v1
  rpcs:
    - name: SetStock
      request:
        hotel_id: string
        room_type: string
        date: string
        quantity: int32
      response: empty
      description: |
        Sets the total stock for one (hotel_id, room_type, date) key. The
        new total overrides any prior total; sold and held counts are
        preserved.

    - name: Hold
      request:
        hotel_id: string
        room_type: string
        date: string
        quantity: int32
      response:
        hold_id: string
      description: |
        Reserves `quantity` units for the key. Returns an opaque hold_id.
        Reduces available stock for subsequent calls.

    - name: Commit
      request:
        hold_id: string
      response: empty
      description: |
        Converts a held reservation to sold. The hold is consumed.

    - name: Release
      request:
        hold_id: string
      response: empty
      description: |
        Releases a held reservation. The held quantity returns to available.

error_semantics:
  - rpc: SetStock
    condition: quantity is negative
    code: InvalidArgument

  - rpc: Hold
    condition: quantity is non-positive
    code: InvalidArgument
  - rpc: Hold
    condition: no stock record exists for (hotel_id, room_type, date)
    code: NotFound
  - rpc: Hold
    condition: insufficient available stock (total - sold - held < quantity)
    code: FailedPrecondition

  - rpc: Commit
    condition: unknown hold_id
    code: NotFound
  - rpc: Commit
    condition: hold has already been committed or released
    code: FailedPrecondition

  - rpc: Release
    condition: unknown hold_id
    code: NotFound
  - rpc: Release
    condition: hold has already been committed or released
    code: FailedPrecondition

  # ── v2: concurrency contract (locked down by server_v2_test.go) ──────
  # When N concurrent Hold calls race for the last available unit, the
  # backing store MUST serialize them so that exactly one succeeds and
  # the rest get FailedPrecondition. Implementations using the Postgres
  # backend do this by acquiring a row-level lock on the stock row
  # (SELECT … FOR UPDATE) inside the Hold transaction.

state:
  # v2 default backend is Postgres; the v1 in-memory backend is kept as
  # an alternate implementation behind the same `Store` interface so
  # the v1 test file (server_test.go) continues to pin its behavior.
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
      schema_source: services/inventory/migrations/0001_init.sql
      schema_applied_by: external   # not by the service at startup; loaded
                                    # externally (compose init script in
                                    # tests, a migration tool in production)

  # Logical schema. Both backends must present the same observable
  # behavior for the v1 RPC contract; the table definitions below match
  # the in-memory map shape one-to-one.
  stores:
    stock:
      key:
        - hotel_id: text
        - room_type: text
        - date: text
      value:
        total: int32         # set by SetStock
        sold: int32          # incremented on Commit
        held: int32          # incremented on Hold, decremented on Commit/Release
    holds:
      key:
        - hold_id: text      # opaque server-generated id
      value:
        hotel_id: text
        room_type: text
        date: text
        quantity: int32
        status: text         # held | committed | released

dependent_interface: []
