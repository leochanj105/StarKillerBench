# Build context = repo root (services/admin/Makefile passes ../..), so the
# Dockerfile can COPY admin and each upstream it imports.
FROM golang:1.25.4 AS build
WORKDIR /src

# admin and its upstreams. Do NOT copy go.work from the repo root — it may
# reference services not present in this image. Generate a minimal one inline.
COPY services/admin ./services/admin
COPY services/geo ./services/geo
COPY services/profile ./services/profile
COPY services/inventory ./services/inventory
COPY services/pricing ./services/pricing
COPY services/ads ./services/ads

RUN printf 'go 1.25.4\n\nuse (\n\t./services/admin\n\t./services/geo\n\t./services/profile\n\t./services/inventory\n\t./services/pricing\n\t./services/ads\n)\n' > /src/go.work

WORKDIR /src/services/admin
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/admin ./cmd/admin

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/admin /admin
EXPOSE 50051 8080
ENTRYPOINT ["/admin"]
