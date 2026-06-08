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

# services/notification/SPEC.yaml

summary: |
  notification turns booking lifecycle events into outbound messages
  (email/sms/push stubs). In the full system it is an async worker consuming
  NATS subjects; v1 is a synchronous placeholder: a single Notify RPC takes
  one event and returns the message(s) it would send. Stateless and
  deterministic — no broker, no worker pool, no sink.

  v1 message mapping (one message per event):
    - "booking.confirmed" → [{channel: "email", body: "...confirmed..."}]
    - "booking.cancelled" → [{channel: "email", body: "...cancelled..."}]
  Each body references the booking_id.

  Deferred to v2 (see VERSIONS.md): real async consumption from NATS
  JetStream (durable consumers of booking.confirmed / booking.cancelled /
  review.posted), a configurable worker pool, queue-depth metrics, the 1–3
  messages-per-event fan-out, and the redelivery/retry failure mode. The v2
  upgrade also requires booking to *emit* events (booking v3), so the two
  land together as the event-layer milestone.

file_organization:
  service_dir: services/notification
  go_module: agentbench/services/notification
  directories:
    - services/notification/cmd/notification
    - services/notification/internal/server
    - services/notification/api/v1
    - services/notification/proto/v1

interface:
  protocol: grpc
  service: NotificationService
  package: notification.v1
  rpcs:
    - name: Notify
      request:
        event_type: string
        user_id: string
        booking_id: string
      response:
        messages: repeated Message
      description: |
        Generates the outbound messages for one event and returns them. The
        response's `messages` is a repeated Message, where Message has
        fields:
          channel: string   # v1 always "email"
          body:    string    # references the booking_id
        event_type must be one of "booking.confirmed" or "booking.cancelled".

error_semantics:
  - rpc: Notify
    condition: event_type, user_id, or booking_id is empty
    code: InvalidArgument
  - rpc: Notify
    condition: event_type is not a recognized event ("booking.confirmed" or "booking.cancelled")
    code: InvalidArgument

state:
  kind: stateless
  lifetime: per_process

dependent_interface: []
