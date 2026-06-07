# Multi-stage build for the geo service.
FROM golang:1.25 AS build
WORKDIR /src

COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/geo ./cmd/geo

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/geo /geo
EXPOSE 50051
EXPOSE 8080
ENTRYPOINT ["/geo"]
