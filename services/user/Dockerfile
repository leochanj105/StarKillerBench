# Multi-stage build for the user service.
FROM golang:1.25.4 AS build
WORKDIR /src

COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/user ./cmd/user

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/user /user
EXPOSE 50051 8080
ENTRYPOINT ["/user"]
