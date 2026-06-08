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

# services/admin/SPEC.yaml

summary: |
  admin is the hotelier-facing extranet: a second front door (distinct from
  the guest frontend) for setting up hotels and their commercial data. It
  owns no state — every RPC validates input and fans out to the service that
  owns the data.

  v1 fan-out:
    - UpsertHotel  → geo.UpsertHotel (coords) + profile.UpsertProfile (metadata)
    - SetInventory → inventory.SetStock
    - SetRatePlan  → pricing.SetRatePlan
    - SetCampaign  → ads.SetCampaign (campaign_id propagated back)
  An upstream error is propagated to the caller.

  Deferred to v2: admin-scope auth (verify an admin token via auth before
  any write); ListBookings(hotel_id) (needs a booking query by hotel, which
  booking does not yet expose).

file_organization:
  service_dir: services/admin
  go_module: agentbench/services/admin
  directories:
    - services/admin/cmd/admin
    - services/admin/internal/server
    - services/admin/api/v1
    - services/admin/proto/v1

interface:
  protocol: grpc
  service: AdminService
  package: admin.v1
  rpcs:
    - name: UpsertHotel
      request:
        hotel_id: string
        name: string
        address: string
        lat: double
        lng: double
        room_types: repeated string
      response: empty
      description: |
        Registers/updates a hotel: forwards coordinates to geo.UpsertHotel
        and metadata (name, address, room_types) to profile.UpsertProfile.

    - name: SetInventory
      request:
        hotel_id: string
        room_type: string
        date: string
        total: int32
      response: empty
      description: |
        Sets total stock for a (hotel, room_type, date) via inventory.SetStock
        (total maps to the inventory quantity).

    - name: SetRatePlan
      request:
        hotel_id: string
        room_type: string
        nightly_rate: int64
        currency: string
      response: empty
      description: |
        Sets the nightly rate via pricing.SetRatePlan.

    - name: SetCampaign
      request:
        advertiser_id: string
        hotel_id: string
        daily_budget: int64
        bid: int64
      response:
        campaign_id: string
      description: |
        Creates an ad campaign via ads.SetCampaign and returns its
        campaign_id.

error_semantics:
  - rpc: UpsertHotel
    condition: hotel_id or name is empty
    code: InvalidArgument
  - rpc: UpsertHotel
    condition: an upstream (geo or profile) call fails
    code: propagated from the upstream

  - rpc: SetInventory
    condition: hotel_id or room_type is empty
    code: InvalidArgument
  - rpc: SetInventory
    condition: inventory.SetStock fails
    code: propagated from the upstream

  - rpc: SetRatePlan
    condition: hotel_id or room_type is empty
    code: InvalidArgument
  - rpc: SetRatePlan
    condition: pricing.SetRatePlan fails
    code: propagated from the upstream

  - rpc: SetCampaign
    condition: advertiser_id or hotel_id is empty
    code: InvalidArgument
  - rpc: SetCampaign
    condition: ads.SetCampaign fails
    code: propagated from the upstream

state:
  kind: stateless
  lifetime: per_process

dependent_interface: [geo, profile, inventory, pricing, ads]
