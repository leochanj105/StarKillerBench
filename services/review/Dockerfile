# syntax=docker/dockerfile:1

# Build context = service dir (leaf service, no cross-service deps).
FROM golang:1.25.4 AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/review ./cmd/review

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/review /review
EXPOSE 50051 8080
ENTRYPOINT ["/review"]
