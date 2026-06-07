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

# services/geo/SPEC.yaml

summary: |
  geo answers spatial "which hotels are near here" queries for the search
  fan-out. v1 is in-memory: hotel coordinates are seeded via UpsertHotel and
  Nearby does a linear great-circle (haversine) scan over them. State resets
  on restart.

  The realistic version rebuilds a spatial index (R-tree / geohash buckets)
  from MongoDB at startup and serves read-only at runtime; that is deferred
  to a later iteration. The UpsertHotel write RPC is the v1 seed path (in the
  full system, admin updates drive a background reload instead).

file_organization:
  service_dir: services/geo
  go_module: agentbench/services/geo
  directories:
    - services/geo/cmd/geo
    - services/geo/internal/server
    - services/geo/api/v1
    - services/geo/proto/v1

interface:
  protocol: grpc
  service: GeoService
  package: geo.v1
  rpcs:
    - name: UpsertHotel
      request:
        hotel_id: string
        lat: double
        lng: double
      response: empty
      description: |
        Seeds or updates one hotel's coordinates. Overwrites any prior
        coordinates for the same hotel_id.

    - name: Nearby
      request:
        lat: double
        lng: double
        radius_km: double
      response:
        hotel_ids: repeated string
      description: |
        Returns the ids of all seeded hotels whose great-circle (haversine)
        distance from (lat, lng) is <= radius_km. Empty list if none. Order
        is unspecified.

error_semantics:
  - rpc: UpsertHotel
    condition: hotel_id is empty
    code: InvalidArgument
  - rpc: UpsertHotel
    condition: lat outside [-90, 90] or lng outside [-180, 180]
    code: InvalidArgument

  - rpc: Nearby
    condition: lat outside [-90, 90] or lng outside [-180, 180]
    code: InvalidArgument
  - rpc: Nearby
    condition: radius_km is non-positive
    code: InvalidArgument

state:
  kind: in_memory
  lifetime: per_process
  stores:
    hotels:
      kind: map
      key:
        name: hotel_id
        type: string
      value:
        lat: double
        lng: double

dependent_interface: []
