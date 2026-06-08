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

# services/review/SPEC.yaml

summary: |
  review stores hotel reviews and serves them paginated, plus a per-hotel
  rating aggregate. v1 is in-memory. PostReview appends a review; ListReviews
  returns a page; GetAggregate computes review_count and avg_rating on demand.

  Deferred to v2: Postgres storage; a background aggregator that periodically
  recomputes per-hotel ratings and pushes them into profile's cache (v1
  computes the aggregate synchronously on read instead); the "review only
  after a completed stay" check (needs a booking lookup); photo upload (v1
  accepts none).

file_organization:
  service_dir: services/review
  go_module: agentbench/services/review
  directories:
    - services/review/cmd/review
    - services/review/internal/server
    - services/review/api/v1
    - services/review/proto/v1

interface:
  protocol: grpc
  service: ReviewService
  package: review.v1
  rpcs:
    - name: PostReview
      request:
        hotel_id: string
        user_id: string
        rating: int32
        text: string
      response:
        review_id: string
      description: |
        Appends a review for a hotel and returns its generated review_id.
        rating must be in [1, 5].

    - name: ListReviews
      request:
        hotel_id: string
        page: int32
        page_size: int32
      response:
        reviews: repeated Review
        total_count: int32
      description: |
        Returns one page of a hotel's reviews (page is 1-based) plus the
        total review count for the hotel. An unknown hotel yields an empty
        page with total_count 0. Each element is a Review message with
        fields:
          review_id: string
          hotel_id:  string
          user_id:   string
          rating:    int32
          text:      string

    - name: GetAggregate
      request:
        hotel_id: string
      response:
        review_count: int32
        avg_rating: double
      description: |
        Returns the number of reviews and their mean rating for the hotel.
        A hotel with no reviews yields review_count 0 and avg_rating 0.

error_semantics:
  - rpc: PostReview
    condition: hotel_id or user_id is empty
    code: InvalidArgument
  - rpc: PostReview
    condition: rating is outside [1, 5]
    code: InvalidArgument

  - rpc: ListReviews
    condition: page or page_size is non-positive
    code: InvalidArgument

state:
  kind: in_memory
  lifetime: per_process
  stores:
    reviews:
      kind: map
      key:
        name: hotel_id
        type: string
      value:
        list: repeated Review   # appended in post order
        # Review = {review_id, hotel_id, user_id, rating, text}

dependent_interface: []
