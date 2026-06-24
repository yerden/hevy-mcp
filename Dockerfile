FROM golang:1.25-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/hevy-mcp ./cmd/hevy-mcp

FROM alpine:3.20
RUN apk add --no-cache ca-certificates \
 && adduser -D -H hevy
USER hevy

COPY --from=builder /out/hevy-mcp /usr/local/bin/hevy-mcp

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/hevy-mcp", "--transport", "http"]
