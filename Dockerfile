FROM golang:1.24 AS builder

WORKDIR /app

COPY go.mod ./
RUN go mod download

COPY . .
RUN go build -o /out/partition-maintainer ./cmd/partition-maintainer

FROM gcr.io/distroless/base-debian12

WORKDIR /app
COPY --from=builder /out/partition-maintainer /app/partition-maintainer

ENTRYPOINT ["/app/partition-maintainer"]
