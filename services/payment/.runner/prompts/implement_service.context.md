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

# services/payment/SPEC.yaml

summary: |
  payment is a mock external payment provider in the StarkillerBench Hotel
  Reservation app. It stands in for a real PSP so the booking saga can be
  exercised without an external integration. Not production-realistic; a
  behavior-shaped substitute.

  v2 adds deterministic chaos: reserved `payment_token` values trigger
  specific failure or latency behaviors. This lets the booking saga's
  compensation paths and evaluation harnesses exercise non-happy-path
  flows without nondeterminism.

file_organization:
  service_dir: services/payment
  go_module: agentbench/services/payment
  directories:
    - services/payment/cmd/payment
    - services/payment/internal/server
    - services/payment/api/v1
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

  # ── Chaos-injected errors (v2). See `chaos` below for full semantics. ──
  - rpc: Authorize
    condition: payment_token equals "tok_decline"
    code: FailedPrecondition
  - rpc: Capture
    condition: the auth_id was created from an Authorize whose payment_token was "tok_capture_fail"
    code: FailedPrecondition
  - rpc: Refund
    condition: the auth_id was created from an Authorize whose payment_token was "tok_refund_fail"
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
        chaos_mode: string    # "" | "capture_fail" | "refund_fail"
                              # Set at Authorize from the payment_token; pins the
                              # deterministic failure mode (if any) for later
                              # Capture/Refund calls against this auth_id.
                              # Does NOT affect Void, so Void compensation still
                              # works after a tok_capture_fail Authorize.

dependent_interface: []

# ════════════════════════════════════════════════════════════════════════════
# Chaos (v2): deterministic behavior injection via reserved payment_token
# values. Tokens are matched verbatim (no case folding, no whitespace
# trimming). Any token that doesn't match a reserved value behaves
# identically to v1 — normal happy path. Only Authorize inspects the token;
# Capture/Void/Refund derive their behavior from the auth_id's stored
# chaos_mode (set at Authorize time).
# ════════════════════════════════════════════════════════════════════════════
chaos:
  failure_tokens:
    - token: tok_decline
      effect: |
        Authorize returns FailedPrecondition. No auth_id is allocated and
        no state is written. Mirrors a real PSP card decline.
    - token: tok_capture_fail
      effect: |
        Authorize succeeds, allocates an auth_id, and stores chaos_mode
        = "capture_fail". Capture against that auth_id returns
        FailedPrecondition without changing state; the auth stays in
        `authorized` status so Void still succeeds. Mirrors a captured
        auth that the bank rejects at settlement time, allowing the
        booking saga's Void+Release compensation to run.
    - token: tok_refund_fail
      effect: |
        Authorize and Capture succeed normally; the auth_id stores
        chaos_mode = "refund_fail". Refund against that auth_id returns
        FailedPrecondition on every call, regardless of amount or
        accumulated refunds. Used to exercise the cancellation service's
        error path.

  latency_tokens:
    - pattern: tok_slow_<ms>
      effect: |
        The Authorize call sleeps for <ms> milliseconds before returning,
        then continues with normal success behavior (allocates auth_id,
        writes state, returns OK). <ms> is parsed from the suffix as a
        non-negative decimal integer up to 60000 (60s). A malformed
        suffix (non-numeric, negative, out of range) makes the token
        behave as an ordinary token: no sleep, normal success. Latency
        applies only to the Authorize call that carries the token;
        subsequent Capture/Void/Refund on the resulting auth_id run at
        normal speed (the latency is not stored on the auth).
