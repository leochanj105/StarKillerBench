# Implement `cancellation`

You are implementing the entire `cancellation` service in Go, from scratch, against the YAML spec.

## Source of truth

Two files are in your system context:

- `framework/spec.format.md` — the schema and meaning of `SPEC.yaml`.
- `services/cancellation/SPEC.yaml` — this service's canonical spec.

Treat SPEC.yaml as the single source of truth. Do not read other project docs. Implement only what's in the spec — no chaos knobs, observability, persistence, or other features that aren't declared.

**Tests already exist and are human-audited.** `services/cancellation/internal/server/*_test.go` is pre-written; do not modify, delete, or add to those files. Your implementation must make them pass.

## Produce (one pass; checkers verify each piece)

### 1. gRPC contract

`services/cancellation/proto/v1/cancellation.proto`:

- `syntax = "proto3";`
- `package <spec.interface.package>;`
- `option go_package = "agentbench/services/cancellation/api/v1;cancellationv1";`
- Service named `<spec.interface.service>`.
- One `rpc` per entry in `spec.interface.rpcs`. Request/response messages match the `request:` / `response:` shapes in the spec exactly.

### 2. Go service

In `services/cancellation/`:

- `cmd/cancellation/main.go` — gRPC server on `:50051`, HTTP `/healthz` (returning 200) on `:8080`, plain stdlib `log`, graceful shutdown on SIGTERM. Include a `//go:generate protoc ...` directive so `go generate ./...` produces stubs under `api/v1/`.
- `internal/server/server.go` — one handler per RPC in the proto. Implement the behavior described in each rpc's `description`. Return the gRPC status codes declared in `error_semantics` for the matching `condition`s.

### 3. Unit tests (pre-existing)

The test file `services/cancellation/internal/server/server_test.go` is pre-written and audited. **Do not modify or delete it.** Inspect it to learn the contract your handlers must satisfy — specifically the constructor name (`server.NewServer()`), method signatures, and the gRPC error codes asserted for each negative scenario.

### 4. Build & deploy

- `Makefile` with targets: `build` (`go generate ./... && go build ./...`), `test` (`go test ./...`), `image` (`docker build -t agentbench/cancellation:dev .`).
- `Dockerfile` — multi-stage; final image `gcr.io/distroless/static-debian12`; exposes `50051` and `8080`.
- Update `go.mod` via `go mod tidy`.

### 5. Cross-service imports (only if `spec.dependent_interface` is non-empty)

`spec.dependent_interface` is a list of upstream service names. For each name `<dep>` in that list:

- The upstream's public Go package is visible at `services/<dep>/api/v1/`.
- Import it as:

      import deppb "agentbench/services/<dep>/api/v1"

- **Do NOT add the upstream as a `require` in your `go.mod`**, and do NOT add a `replace` directive. The repo's `go.work` (already in place) puts all services in a Go workspace and resolves cross-module imports locally. A `require agentbench/services/<dep> v0.0.0` line triggers Go's module-path validator and fails the whole workspace with `malformed module path "agentbench/services/<dep>": missing dot in first path element`. The `require` block in your `go.mod` should only contain third-party modules (e.g. `google.golang.org/grpc`).

If `go mod tidy` re-adds a `require agentbench/services/<dep>` line, delete it. The pre-authored test file in `internal/server/server_test.go` shows exactly which upstream RPCs your handlers must call and with what arguments — its mocks define the interface your handlers receive via `NewServer(...)`.

## Constraints

- The proto's RPC set MUST equal `spec.interface.rpcs` exactly — no extras, no omissions.
- Handler error codes MUST match `spec.error_semantics`.
- Tests MUST cover both happy-path per RPC and each entry in `error_semantics`.

## Done

`make build` and `make test` succeed; the docker image builds and the running container answers `/healthz`. The framework's checkers pass.


## Previous attempt failed

```

#5 [internal] load metadata for docker.io/library/golang:1.25
#5 DONE 1.0s

#6 [internal] load .dockerignore
#6 transferring context: 2B done
#6 DONE 0.0s

#7 [stage-1 1/2] FROM gcr.io/distroless/static-debian12:latest@sha256:9c346e4be81b5ca7ff31a0d89eaeade58b0f95cfd3baed1f36083ddb47ca3160
#7 CACHED

#8 [build 1/7] FROM docker.io/library/golang:1.25@sha256:c138bff780910acf4254ab3a6f7ff0f64bbd841f27bd82bfa986fe122c109538
#8 resolve docker.io/library/golang:1.25@sha256:c138bff780910acf4254ab3a6f7ff0f64bbd841f27bd82bfa986fe122c109538 done
#8 DONE 0.0s

#9 [build 2/7] WORKDIR /src
#9 CACHED

#10 [internal] load build context
#10 transferring context: 2B done
#10 DONE 0.0s

#11 [build 5/7] COPY services/cancellation /src/services/cancellation
#11 ERROR: failed to calculate checksum of ref 9dc568b2-6ea3-4a2a-a586-60251a4358f4::xpy1ayvayw4v8c213cmh5uasj: "/services/cancellation": not found

#12 [build 3/7] COPY services/booking /src/services/booking
#12 ERROR: failed to calculate checksum of ref 9dc568b2-6ea3-4a2a-a586-60251a4358f4::xpy1ayvayw4v8c213cmh5uasj: "/services/booking": not found

#13 [build 4/7] COPY services/payment /src/services/payment
#13 ERROR: failed to calculate checksum of ref 9dc568b2-6ea3-4a2a-a586-60251a4358f4::xpy1ayvayw4v8c213cmh5uasj: "/services/payment": not found
------
 > [build 3/7] COPY services/booking /src/services/booking:
------
------
 > [build 4/7] COPY services/payment /src/services/payment:
------
------
 > [build 5/7] COPY services/cancellation /src/services/cancellation:
------
Dockerfile:6
--------------------
   4 |     COPY services/booking /src/services/booking
   5 |     COPY services/payment /src/services/payment
   6 | >>> COPY services/cancellation /src/services/cancellation
   7 |     WORKDIR /src/services/cancellation
   8 |     RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -o /out/cancellation ./cmd/cancellation
--------------------
ERROR: failed to build: failed to solve: failed to compute cache key: failed to calculate checksum of ref 9dc568b2-6ea3-4a2a-a586-60251a4358f4::xpy1ayvayw4v8c213cmh5uasj: "/services/cancellation": not found
make: *** [Makefile:11: image] Error 1

```
