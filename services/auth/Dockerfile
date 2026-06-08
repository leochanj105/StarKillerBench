# syntax=docker/dockerfile:1

# ── build stage ──────────────────────────────────────────────────────────────
FROM golang:1.25.4 AS build
WORKDIR /src

COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/auth ./cmd/auth

# ── final stage ──────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12
COPY --from=build /out/auth /auth
EXPOSE 50051
EXPOSE 8080
ENTRYPOINT ["/auth"]
