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
    - services/payment/internal/genpb
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
dependent_interface:
  - service: booking
    rpc: GetBooking
    purpose: Look up a booking by ID.
```

List of outbound RPC calls this service makes. Each entry: `service` + `rpc` + `purpose` (one line). Empty list `[]` for leaf services.


---

# services/payment/SPEC.yaml

summary: |
  payment is a mock external payment provider in the StarkillerBench Hotel
  Reservation app. It stands in for a real PSP so the booking saga can be
  exercised without an external integration. Not production-realistic; a
  behavior-shaped substitute.

file_organization:
  service_dir: services/payment
  go_module: agentbench/services/payment
  directories:
    - services/payment/cmd/payment
    - services/payment/internal/server
    - services/payment/internal/store
    - services/payment/internal/config
    - services/payment/internal/genpb
    - services/payment/test
    - services/payment/proto/v1

interface:
  protocol: grpc
  service: PaymentService
  package: payment.v1
  rpcs:
    - name: Authorize
      request:
        amount: int64
        currency: string
        payment_token: string
      response:
        auth_id: string
      description: Records a new authorization; returns a unique opaque auth_id.

    - name: Capture
      request:
        auth_id: string
      response: empty
      description: Captures a previously-authorized amount.

    - name: Void
      request:
        auth_id: string
      response: empty
      description: Voids an authorization that was never captured.

    - name: Refund
      request:
        auth_id: string
        amount: int64
      response: empty
      description: Refunds (partial or full) a captured authorization.

error_semantics:
  - rpc: Authorize
    condition: amount is non-positive or currency is empty
    code: InvalidArgument

  - rpc: Capture
    condition: unknown auth_id
    code: NotFound
  - rpc: Capture
    condition: incompatible state (e.g. capturing an already-voided auth)
    code: FailedPrecondition

  - rpc: Void
    condition: unknown auth_id
    code: NotFound
  - rpc: Void
    condition: incompatible state (already captured)
    code: FailedPrecondition

  - rpc: Refund
    condition: unknown auth_id
    code: NotFound
  - rpc: Refund
    condition: refund amount exceeds (captured - already refunded)
    code: FailedPrecondition

state:
  kind: in_memory
  lifetime: per_process       # state is lost on restart, by design
  stores:
    authorizations:
      kind: map
      key:
        name: auth_id
        type: string
      value:
        amount: int64         # the originally authorized amount
        currency: string
        status: string        # values: authorized | captured | voided
        refunded: int64       # total refunded so far; only meaningful after capture

dependent_interface: []
