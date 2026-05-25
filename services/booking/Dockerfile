FROM golang:1.25 AS build

WORKDIR /src

COPY services/booking/go.mod services/booking/go.sum services/booking/
COPY services/inventory/go.mod services/inventory/go.sum services/inventory/
COPY services/payment/go.mod services/payment/go.sum services/payment/
COPY go.work go.work

COPY services/inventory/api services/inventory/api
COPY services/payment/api services/payment/api
COPY services/booking services/booking

WORKDIR /src/services/booking
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/booking ./cmd/booking

FROM gcr.io/distroless/static-debian12

COPY --from=build /out/booking /booking

EXPOSE 50051 8080

ENTRYPOINT ["/booking"]
