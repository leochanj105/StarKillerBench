# Implement `admin`

You are implementing the entire `admin` service in Go, from scratch, against the YAML spec.

## Source of truth

Two files are in your system context:

- `framework/spec.format.md` — the schema and meaning of `SPEC.yaml`.
- `services/admin/SPEC.yaml` — this service's canonical spec.

Treat SPEC.yaml as the single source of truth. Do not read other project docs. Implement only what's in the spec — no chaos knobs, observability, persistence, or other features that aren't declared.

**Tests already exist and are human-audited.** `services/admin/internal/server/*_test.go` is pre-written; do not modify, delete, or add to those files. Your implementation must make them pass.

## Produce (one pass; checkers verify each piece)

### 1. gRPC contract

`services/admin/proto/v1/admin.proto`:

- `syntax = "proto3";`
- `package <spec.interface.package>;`
- `option go_package = "agentbench/services/admin/api/v1;adminv1";`
- Service named `<spec.interface.service>`.
- One `rpc` per entry in `spec.interface.rpcs`. Request/response messages match the `request:` / `response:` shapes in the spec exactly.

### 2. Go service

In `services/admin/`:

- `cmd/admin/main.go` — gRPC server on `:50051`, HTTP `/healthz` (returning 200) on `:8080`, plain stdlib `log`, graceful shutdown on SIGTERM. Include a `//go:generate protoc ...` directive so `go generate ./...` produces stubs under `api/v1/`.
- `internal/server/server.go` — one handler per RPC in the proto. Implement the behavior described in each rpc's `description`. Return the gRPC status codes declared in `error_semantics` for the matching `condition`s.

### 3. Unit tests (pre-existing)

All test files matching `services/admin/internal/server/*_test.go` are pre-written and audited. **Do not modify, delete, or add to them.** There may be multiple — one per version increment (e.g. `server_test.go` for v1, `server_v2_test.go` for chaos features). Inspect every `*_test.go` file in that directory to learn the full contract your handlers must satisfy: constructor name (`server.NewServer()`), method signatures, gRPC error codes for each negative scenario, and any version-specific behavior (failure injection, latency, etc.). Your implementation must make every test pass.

### 4. Build & deploy

- `Makefile` with targets: `build` (`go generate ./... && go build ./...`), `test` (`go test ./...`), `image`.
  - If `spec.dependent_interface` is empty: `image` target is `docker build -t agentbench/admin:dev .` (build context = service dir).
  - If `spec.dependent_interface` is non-empty: `image` target is `docker build -t agentbench/admin:dev -f Dockerfile ../..` (build context = repo root, needed so the Dockerfile can COPY sibling services).
- `Dockerfile` — multi-stage; final image `gcr.io/distroless/static-debian12`; exposes `50051` and `8080`.
  - For cross-service builds, **do NOT `COPY go.work go.work`** from the repo root — that file may reference services your container doesn't include, breaking the build whenever a new service is added. Instead, COPY only this service and each entry in `spec.dependent_interface`, then generate a minimal `go.work` inline, e.g. `RUN printf 'go 1.25.4\n\nuse (\n\t./services/admin\n\t./services/<dep>\n)\n' > /src/go.work`.
- Update `go.mod` via `go mod tidy`.

### 5. Cross-service imports (only if `spec.dependent_interface` is non-empty)

`spec.dependent_interface` is a list of upstream service names. For each name `<dep>` in that list:

- The upstream's public Go package is visible at `services/<dep>/api/v1/`.
- Import it as:

      import deppb "agentbench/services/<dep>/api/v1"

- **Do NOT add the upstream as a `require` in your `go.mod`**, and do NOT add a `replace` directive. The repo's `go.work` (already in place) puts all services in a Go workspace and resolves cross-module imports locally. A `require agentbench/services/<dep> v0.0.0` line triggers Go's module-path validator and fails the whole workspace with `malformed module path "agentbench/services/<dep>": missing dot in first path element`. The `require` block in your `go.mod` should only contain third-party modules (e.g. `google.golang.org/grpc`).

If `go mod tidy` re-adds a `require agentbench/services/<dep>` line, delete it. The pre-authored test files in `internal/server/*_test.go` show exactly which upstream RPCs your handlers must call and with what arguments — their mocks define the interface your handlers receive via `NewServer(...)`.

## Constraints

- The proto's RPC set MUST equal `spec.interface.rpcs` exactly — no extras, no omissions.
- Handler error codes MUST match `spec.error_semantics`.
- Tests MUST cover both happy-path per RPC and each entry in `error_semantics`.

## Done

`make build` and `make test` succeed; the docker image builds and the running container answers `/healthz`. The framework's checkers pass.
