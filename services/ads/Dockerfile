# Multi-stage build for the ads service.
FROM golang:1.25.4 AS build
WORKDIR /src

COPY go.mod go.sum* ./
RUN go mod download || true

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/ads ./cmd/ads

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/ads /ads
EXPOSE 50051 8080
ENTRYPOINT ["/ads"]
