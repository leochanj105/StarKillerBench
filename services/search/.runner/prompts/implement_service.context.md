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

# services/search/SPEC.yaml

summary: |
  search is the read-path aggregator: given a location and stay dates, it
  fans out to geo, profile, and pricing and returns a ranked list of hotels
  with their cheapest priced room. It is the read-path twin of booking
  (booking orchestrates writes; search orchestrates reads). Stateless.

  v1 pipeline per request:
    1. geo.Nearby(lat, lng, radius_km)            → candidate hotel_ids
    2. for each hotel: profile.GetProfile(hotel_id) → name, address, room_types
    3. for each room_type: pricing.Quote(...)       → price that room for the stay
    4. keep the cheapest priceable room per hotel; omit hotels with none
    5. rank results by total price ascending
  guests and the stay dates are passed through to pricing unchanged.

  Deferred to later iterations (these would change upstreams, so they are not
  in v1): real availability filtering via an inventory CheckAvailability RPC;
  sponsored placements via ads; result caching; multi-factor ranking
  (rating, distance) and filters.

file_organization:
  service_dir: services/search
  go_module: agentbench/services/search
  directories:
    - services/search/cmd/search
    - services/search/internal/server
    - services/search/api/v1
    - services/search/proto/v1

interface:
  protocol: grpc
  service: SearchService
  package: search.v1
  rpcs:
    - name: Search
      request:
        lat: double
        lng: double
        radius_km: double
        check_in: string
        check_out: string
        guests: int32
      response:
        results: repeated SearchResult
      description: |
        Returns hotels near (lat, lng) within radius_km that have at least
        one priceable room for the stay, ranked by total price ascending.
        Empty list if none. Each element is a SearchResult message with
        fields:
          hotel_id:     string
          name:         string
          address:      string
          nightly_rate: int64   # nightly rate of the cheapest priceable room
          total:        int64   # total stay price of that room (from pricing)
          currency:     string
        A hotel returned by geo whose profile is missing, or whose room
        types are all unpriceable (pricing returns NotFound), is omitted —
        that is not a Search error. Input is validated before any upstream
        call.

error_semantics:
  - rpc: Search
    condition: lat outside [-90, 90] or lng outside [-180, 180]
    code: InvalidArgument
  - rpc: Search
    condition: radius_km is non-positive
    code: InvalidArgument
  - rpc: Search
    condition: check_in or check_out is not a valid YYYY-MM-DD date, or check_out is not strictly after check_in
    code: InvalidArgument
  - rpc: Search
    condition: guests is non-positive
    code: InvalidArgument
  - rpc: Search
    condition: geo.Nearby returns an error
    code: Internal

state:
  kind: stateless
  lifetime: per_process

dependent_interface: [geo, profile, pricing]
