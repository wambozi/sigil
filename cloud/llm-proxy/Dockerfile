FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o llm-proxy .

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/llm-proxy /usr/local/bin/llm-proxy
EXPOSE 8081
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- http://localhost:8081/healthz || exit 1
ENTRYPOINT ["llm-proxy"]
