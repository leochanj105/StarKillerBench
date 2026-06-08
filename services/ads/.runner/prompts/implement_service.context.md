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

# services/ads/SPEC.yaml

summary: |
  ads is a mock advertising bidder. Advertisers set campaigns (a bid and a
  daily budget for a hotel); SelectSponsored runs a second-price auction to
  fill sponsored slots and charges winners against their budgets. Called by
  search to mix sponsored placements into results. v1 is in-memory.

  v1 auction (deterministic):
    - Eligible campaigns = those with remaining_budget > 0.
    - Sort eligible by bid descending; the top `slot_count` are winners.
    - Each winner's price is the next-lower bid among eligible campaigns, or,
      if it is the lowest/only bidder, its own bid (first-price fallback for
      the last slot).
    - A winner is charged its price against remaining_budget. If it cannot
      afford its price (remaining_budget < price), it is skipped (not charged,
      not returned) and the slot is left to the next eligible campaign.
    - remaining_budget never goes negative.

  Deferred to v2: Postgres campaign store + Redis current-day spend with
  optimistic concurrency; NATS impression/click event emission; the
  attribution worker that consumes booking.confirmed to credit campaigns. v1
  LogImpression/LogClick accept and acknowledge but do not persist or emit.

file_organization:
  service_dir: services/ads
  go_module: agentbench/services/ads
  directories:
    - services/ads/cmd/ads
    - services/ads/internal/server
    - services/ads/api/v1
    - services/ads/proto/v1

interface:
  protocol: grpc
  service: AdsService
  package: ads.v1
  rpcs:
    - name: SetCampaign
      request:
        advertiser_id: string
        hotel_id: string
        daily_budget: int64
        bid: int64
      response:
        campaign_id: string
      description: |
        Creates a campaign (bid + daily budget, in minor units) for a hotel
        and returns its generated campaign_id.

    - name: SelectSponsored
      request:
        slot_count: int32
      response:
        slots: repeated SponsoredSlot
      description: |
        Runs the second-price auction (see summary) and returns the winning
        slots, ordered by bid descending. Charges each winner its price.
        Each element is a SponsoredSlot message with fields:
          hotel_id:    string
          campaign_id: string
          price:       int64
        Returns an empty list if no campaign is eligible/affordable.

    - name: LogImpression
      request:
        hotel_id: string
        query_id: string
      response: empty
      description: |
        Fire-and-forget impression ingestion. v1 acknowledges only.

    - name: LogClick
      request:
        hotel_id: string
        query_id: string
      response: empty
      description: |
        Fire-and-forget click ingestion. v1 acknowledges only.

error_semantics:
  - rpc: SetCampaign
    condition: advertiser_id or hotel_id is empty
    code: InvalidArgument
  - rpc: SetCampaign
    condition: daily_budget or bid is non-positive
    code: InvalidArgument

  - rpc: SelectSponsored
    condition: slot_count is non-positive
    code: InvalidArgument

  - rpc: LogImpression
    condition: hotel_id is empty
    code: InvalidArgument
  - rpc: LogClick
    condition: hotel_id is empty
    code: InvalidArgument

state:
  kind: in_memory
  lifetime: per_process
  stores:
    campaigns:
      kind: map
      key:
        name: campaign_id
        type: string
      value:
        advertiser_id: string
        hotel_id: string
        bid: int64
        remaining_budget: int64   # decremented on win; never negative

dependent_interface: []
