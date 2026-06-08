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

# services/user/SPEC.yaml

summary: |
  user owns non-credential account state: preferences and a loyalty points
  balance. (Identity — register/login — lives in auth.) v1 is in-memory.

  The systems-relevant behavior is the loyalty balance: AccrueOnBooking is a
  read-modify-write on a per-user counter, and concurrent accruals for the
  same user must serialize without losing updates. v1 enforces this with a
  mutex (the in-memory analog of the realistic version's optimistic-
  concurrency version column in Postgres, which is deferred).

  Deferred to v2: Postgres with a version column for optimistic concurrency,
  Redis cache, ListTrips (calls booking), ListPaymentMethods, and async
  accrual via a NATS booking.confirmed consumer (v1 exposes AccrueOnBooking
  as a direct RPC instead).

file_organization:
  service_dir: services/user
  go_module: agentbench/services/user
  directories:
    - services/user/cmd/user
    - services/user/internal/server
    - services/user/api/v1
    - services/user/proto/v1

interface:
  protocol: grpc
  service: UserService
  package: user.v1
  rpcs:
    - name: CreateUser
      request:
        user_id: string
      response: empty
      description: |
        Creates a user record with zero points and empty preferences.

    - name: GetUser
      request:
        user_id: string
      response:
        user_id: string
        currency: string
        locale: string
        points: int64
      description: |
        Returns the user's preferences and current points balance.

    - name: UpdatePreferences
      request:
        user_id: string
        currency: string
        locale: string
      response: empty
      description: |
        Overwrites the user's preference fields.

    - name: GetPoints
      request:
        user_id: string
      response:
        points: int64
      description: |
        Returns the current loyalty points balance.

    - name: AccrueOnBooking
      request:
        user_id: string
        amount: int64
      response:
        points: int64
      description: |
        Adds loyalty points for a booking of `amount` (minor units): points
        earned = amount / 100 (integer division). Atomically increments the
        balance and returns the new total. Concurrent calls for the same
        user must not lose updates.

error_semantics:
  - rpc: CreateUser
    condition: user_id is empty
    code: InvalidArgument
  - rpc: CreateUser
    condition: a user with that user_id already exists
    code: AlreadyExists

  - rpc: GetUser
    condition: unknown user_id
    code: NotFound

  - rpc: UpdatePreferences
    condition: user_id is empty
    code: InvalidArgument
  - rpc: UpdatePreferences
    condition: unknown user_id
    code: NotFound

  - rpc: GetPoints
    condition: unknown user_id
    code: NotFound

  - rpc: AccrueOnBooking
    condition: user_id is empty
    code: InvalidArgument
  - rpc: AccrueOnBooking
    condition: amount is non-positive
    code: InvalidArgument
  - rpc: AccrueOnBooking
    condition: unknown user_id
    code: NotFound

state:
  kind: in_memory
  lifetime: per_process
  stores:
    users:
      kind: map
      key:
        name: user_id
        type: string
      value:
        currency: string
        locale: string
        points: int64        # loyalty balance; RMW under a lock
dependent_interface: []
