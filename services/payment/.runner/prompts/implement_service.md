# Implement `payment`

You are implementing the entire `payment` service in Go, from scratch, against the YAML spec.

## Source of truth

Two files are in your system context:

- `framework/spec.format.md` — the schema and meaning of `SPEC.yaml`.
- `services/payment/SPEC.yaml` — this service's canonical spec.

Treat SPEC.yaml as the single source of truth. Do not read other project docs. Implement only what's in the spec — no chaos knobs, observability, persistence, or other features that aren't declared.

**Tests already exist and are human-audited.** `services/payment/internal/server/*_test.go` is pre-written; do not modify, delete, or add to those files. Your implementation must make them pass.

## Produce (one pass; checkers verify each piece)

### 1. gRPC contract

`services/payment/proto/v1/payment.proto`:

- `syntax = "proto3";`
- `package <spec.interface.package>;`
- `option go_package = "agentbench/services/payment/internal/genpb/v1;paymentv1";`
- Service named `<spec.interface.service>`.
- One `rpc` per entry in `spec.interface.rpcs`. Request/response messages match the `request:` / `response:` shapes in the spec exactly.

### 2. Go service

In `services/payment/`:

- `cmd/payment/main.go` — gRPC server on `:50051`, HTTP `/healthz` (returning 200) on `:8080`, plain stdlib `log`, graceful shutdown on SIGTERM. Include a `//go:generate protoc ...` directive so `go generate ./...` produces stubs under `internal/genpb/`.
- `internal/server/server.go` — one handler per RPC in the proto. Implement the behavior described in each rpc's `description`. Return the gRPC status codes declared in `error_semantics` for the matching `condition`s.

### 3. Unit tests (pre-existing)

The test file `services/payment/internal/server/server_test.go` is pre-written and audited. **Do not modify or delete it.** Inspect it to learn the contract your handlers must satisfy — specifically the constructor name (`server.NewServer()`), method signatures, and the gRPC error codes asserted for each negative scenario.

### 4. Build & deploy

- `Makefile` with targets: `build` (`go generate ./... && go build ./...`), `test` (`go test ./...`), `image` (`docker build -t agentbench/payment:dev .`).
- `Dockerfile` — multi-stage; final image `gcr.io/distroless/static-debian12`; exposes `50051` and `8080`.
- Update `go.mod` via `go mod tidy`.

## Constraints

- The proto's RPC set MUST equal `spec.interface.rpcs` exactly — no extras, no omissions.
- Handler error codes MUST match `spec.error_semantics`.
- Tests MUST cover both happy-path per RPC and each entry in `error_semantics`.

## Done

`make build` and `make test` succeed; the docker image builds and the running container answers `/healthz`. The framework's checkers pass.
