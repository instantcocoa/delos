# Build stage
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates

# Install buf
RUN go install github.com/bufbuild/buf/cmd/buf@v1.47.2

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy proto files and generate code
COPY buf.yaml buf.gen.yaml ./
COPY proto/ proto/
RUN buf generate

# Copy source
COPY . .

# Build
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /bin/server ./services/prompt/cmd/server

# Final stage
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /bin/server /app/server

# Run as non-root
RUN adduser -D -u 1000 delos
USER delos

EXPOSE 9002

ENTRYPOINT ["/app/server"]
