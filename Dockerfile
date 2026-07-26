# syntax=docker/dockerfile:1.7

FROM golang:1.25-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Populate the embedded public exercise catalog if a Hevy API key is provided
# as a BuildKit secret. Without the secret the build still succeeds, but the
# resulting binary embeds only the `[]` stub — hevy_search_exercise_templates
# will return no results and the process logs a warning at startup.
#
# Invoke like:  docker build --secret id=hevy_key,env=HEVY_API_KEY -t hevy-mcp .
#           or: fly deploy --build-secret hevy_key="$HEVY_API_KEY"
RUN --mount=type=secret,id=hevy_key \
    if [ -s /run/secrets/hevy_key ]; then \
      echo "generating public exercise catalog"; \
      HEVY_API_KEY="$(cat /run/secrets/hevy_key)" go generate ./internal/exercises/...; \
    else \
      echo "no hevy_key secret provided; embedding empty catalog stub"; \
    fi

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/hevy-mcp ./cmd/hevy-mcp

FROM alpine:3.20
RUN apk add --no-cache ca-certificates \
 && adduser -D -H hevy
USER hevy

COPY --from=builder /out/hevy-mcp /usr/local/bin/hevy-mcp

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/hevy-mcp", "--transport", "http"]
