# Build Stage
FROM golang:1.23-alpine AS builder

# Install ZMQ and build tools
RUN apk add --no-cache git gcc musl-dev g++ zeromq-dev pkgconfig

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build the binary
RUN go build -o pricing-service-bin cmd/pricing-service/main.go

# Final Stage
FROM alpine:latest

# Install runtime ZMQ dependencies
RUN apk add --no-cache zeromq libstdc++ libc6-compat tzdata

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/pricing-service-bin /app/pricing-service-bin

# Copy config folder
COPY --from=builder /app/config /app/config

# Run the binary
CMD ["/app/pricing-service-bin"]
