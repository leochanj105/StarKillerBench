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

# services/auth/SPEC.yaml

summary: |
  auth issues and verifies user credentials and tokens. It is on the hot path
  for every authenticated request (frontend/admin call VerifyToken per
  request), so it exists to exercise crypto-on-the-hot-path behavior.

  v1 is in-memory and deterministic:
    - Users are stored in a map keyed by email, with a salted password hash
      (v1 uses SHA-256 over salt+password; the realistic version uses
      Argon2id — deferred).
    - Access tokens are stateless JWTs signed with HMAC-SHA256 over a secret
      read from AUTH_JWT_SECRET (a dev default is used if unset). Claims:
      subject = user_id, scope, issued-at, expiry. VerifyToken validates the
      signature and expiry only — no store lookup.
    - Refresh tokens are opaque random strings held server-side in a
      sessions map; Refresh mints a new access token, Logout revokes the
      session.
  All v1 users are issued the "guest" scope.

  Deferred to later iterations: Argon2id hashing, RS256 signing with a
  published JWKS, Redis-backed sessions + revocation list, login rate
  limiting, and distinct admin-scope issuance.

file_organization:
  service_dir: services/auth
  go_module: agentbench/services/auth
  directories:
    - services/auth/cmd/auth
    - services/auth/internal/server
    - services/auth/api/v1
    - services/auth/proto/v1

interface:
  protocol: grpc
  service: AuthService
  package: auth.v1
  rpcs:
    - name: Register
      request:
        email: string
        password: string
      response: empty
      description: |
        Creates a new user with the "guest" scope. Stores a salted password
        hash, never the plaintext.

    - name: Login
      request:
        email: string
        password: string
      response:
        access_token: string
        refresh_token: string
      description: |
        Verifies the password and issues a fresh access token (JWT) and a
        new refresh token (opaque, server-side session).

    - name: VerifyToken
      request:
        access_token: string
      response:
        user_id: string
        scope: string
      description: |
        Validates the access token's signature and expiry and returns its
        claims. Stateless — no session lookup.

    - name: Refresh
      request:
        refresh_token: string
      response:
        access_token: string
      description: |
        Issues a new access token for the session identified by a valid,
        non-revoked refresh token.

    - name: Logout
      request:
        refresh_token: string
      response: empty
      description: |
        Revokes the session for the given refresh token so it can no longer
        be refreshed. Idempotent: revoking an unknown or already-revoked
        token still returns success (it does not reveal whether the token
        existed).

error_semantics:
  - rpc: Register
    condition: email or password is empty
    code: InvalidArgument
  - rpc: Register
    condition: a user with that email already exists
    code: AlreadyExists

  - rpc: Login
    condition: email or password is empty
    code: InvalidArgument
  - rpc: Login
    condition: unknown email or wrong password
    code: Unauthenticated

  - rpc: VerifyToken
    condition: access_token is empty
    code: InvalidArgument
  - rpc: VerifyToken
    condition: token is malformed, has a bad signature, or is expired
    code: Unauthenticated

  - rpc: Refresh
    condition: refresh_token is empty
    code: InvalidArgument
  - rpc: Refresh
    condition: refresh_token is unknown or revoked
    code: Unauthenticated

  - rpc: Logout
    condition: refresh_token is empty
    code: InvalidArgument

state:
  kind: in_memory
  lifetime: per_process
  stores:
    users:
      kind: map
      key:
        name: email
        type: string
      value:
        user_id: string
        password_hash: string   # SHA-256 over salt+password (v1)
        salt: string
        scope: string           # "guest" in v1
    sessions:
      kind: map
      key:
        name: refresh_token
        type: string
      value:
        user_id: string
        scope: string

dependent_interface: []
