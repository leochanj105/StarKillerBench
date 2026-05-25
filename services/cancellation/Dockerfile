# syntax=docker/dockerfile:1
FROM golang:1.25 AS build
WORKDIR /src
COPY services/booking /src/services/booking
COPY services/payment /src/services/payment
COPY services/cancellation /src/services/cancellation
RUN printf 'go 1.25.4\n\nuse (\n\t./services/booking\n\t./services/payment\n\t./services/cancellation\n)\n' > /src/go.work
WORKDIR /src/services/cancellation
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/cancellation ./cmd/cancellation

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/cancellation /cancellation
EXPOSE 50051 8080
ENTRYPOINT ["/cancellation"]
