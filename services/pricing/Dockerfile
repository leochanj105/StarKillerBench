# syntax=docker/dockerfile:1

# Build context = service dir (services/pricing); pricing is a leaf service.
FROM golang:1.25.4 AS build
WORKDIR /src

COPY go.mod ./
COPY go.sum ./
RUN go mod download || true

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/pricing ./cmd/pricing

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/pricing /pricing
EXPOSE 50051 8080
ENTRYPOINT ["/pricing"]
