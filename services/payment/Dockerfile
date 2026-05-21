# syntax=docker/dockerfile:1
FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/payment ./cmd/payment

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/payment /payment
EXPOSE 50051 8080
ENTRYPOINT ["/payment"]
