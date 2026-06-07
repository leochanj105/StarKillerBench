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

# services/pricing/SPEC.yaml

summary: |
  pricing computes a stay quote for a (hotel, room_type, date range). v1 is
  in-memory and deterministic: a nightly rate is seeded per (hotel, room_type)
  via SetRatePlan, and Quote multiplies it by the number of nights, then adds
  a fixed-rate tax and a flat booking fee. State resets on restart.

  The realistic version is CPU-heavy with a hot Redis cache: length-of-stay
  multiplier, day-of-week factor, a demand factor materialized by booking
  (async via NATS), promo codes, and a batch endpoint (BatchQuote) for the
  search fan-in. All of that is deferred to later iterations. SetRatePlan is
  the v1 seed path (admin's SetRatePlan drives it in the full system).

  Money is in integer minor units (e.g. cents). The v1 formula is:
    subtotal = nightly_rate * nights
    taxes    = subtotal / 10        (10%, integer division)
    fees     = 1500                 (flat per-stay booking fee)
    total    = subtotal + taxes + fees
  nights is the number of nights between check_in and check_out (dates are
  "YYYY-MM-DD"; check_out must be strictly after check_in).

file_organization:
  service_dir: services/pricing
  go_module: agentbench/services/pricing
  directories:
    - services/pricing/cmd/pricing
    - services/pricing/internal/server
    - services/pricing/api/v1
    - services/pricing/proto/v1

interface:
  protocol: grpc
  service: PricingService
  package: pricing.v1
  rpcs:
    - name: SetRatePlan
      request:
        hotel_id: string
        room_type: string
        nightly_rate: int64
        currency: string
      response: empty
      description: |
        Seeds or updates the nightly rate for one (hotel_id, room_type).
        Overwrites any prior rate plan for that key.

    - name: Quote
      request:
        hotel_id: string
        room_type: string
        check_in: string
        check_out: string
        guests: int32
      response:
        nightly_rate: int64
        nights: int32
        subtotal: int64
        taxes: int64
        fees: int64
        total: int64
        currency: string
      description: |
        Returns a priced quote for the stay using the v1 formula in the
        summary. guests is validated (> 0) but does not affect the v1 price.

error_semantics:
  - rpc: SetRatePlan
    condition: hotel_id or room_type is empty
    code: InvalidArgument
  - rpc: SetRatePlan
    condition: nightly_rate is non-positive
    code: InvalidArgument
  - rpc: SetRatePlan
    condition: currency is empty
    code: InvalidArgument

  - rpc: Quote
    condition: no rate plan exists for (hotel_id, room_type)
    code: NotFound
  - rpc: Quote
    condition: check_in or check_out is not a valid YYYY-MM-DD date
    code: InvalidArgument
  - rpc: Quote
    condition: check_out is not strictly after check_in
    code: InvalidArgument
  - rpc: Quote
    condition: guests is non-positive
    code: InvalidArgument

state:
  kind: in_memory
  lifetime: per_process
  stores:
    rate_plans:
      kind: map
      key:
        name: rate_key
        type: string          # "<hotel_id>|<room_type>"
      value:
        nightly_rate: int64
        currency: string

dependent_interface: []
