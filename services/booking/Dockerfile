# syntax=docker/dockerfile:1
FROM golang:1.25 AS build
WORKDIR /src
COPY services/inventory /src/services/inventory
COPY services/payment /src/services/payment
COPY services/booking /src/services/booking
RUN printf 'go 1.25.4\n\nuse (\n\t./services/inventory\n\t./services/payment\n\t./services/booking\n)\n' > /src/go.work
WORKDIR /src/services/booking
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/booking ./cmd/booking

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/booking /booking
EXPOSE 50051 8080
ENTRYPOINT ["/booking"]
