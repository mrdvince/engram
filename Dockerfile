FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o memory-mcp .

FROM alpine:3.20

RUN apk add --no-cache ca-certificates

COPY --from=builder /app/memory-mcp /usr/local/bin/memory-mcp

ENV LIBSQL_URL=http://host.docker.internal:8080

ENTRYPOINT ["/usr/local/bin/memory-mcp"]
