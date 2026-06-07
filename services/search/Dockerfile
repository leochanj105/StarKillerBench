# Build context is the repo root (see Makefile `image` target) so the build can
# COPY the sibling services that search imports.
FROM golang:1.25 AS build
WORKDIR /src

# Copy search and each dependent service.
COPY services/search ./services/search
COPY services/geo ./services/geo
COPY services/profile ./services/profile
COPY services/pricing ./services/pricing

# Generate a minimal workspace covering only the services in this image, so
# adding unrelated services to the repo can't break this build.
RUN printf 'go 1.25.4\n\nuse (\n\t./services/search\n\t./services/geo\n\t./services/profile\n\t./services/pricing\n)\n' > /src/go.work

WORKDIR /src/services/search
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/search ./cmd/search

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/search /search
EXPOSE 50051 8080
ENTRYPOINT ["/search"]
