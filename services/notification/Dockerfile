# Multi-stage build for the notification service.
FROM golang:1.25.4 AS build
WORKDIR /src

COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/notification ./cmd/notification

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/notification /notification
EXPOSE 50051
EXPOSE 8080
ENTRYPOINT ["/notification"]
