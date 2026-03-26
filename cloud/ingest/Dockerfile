FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o ingest-service ./cloud/ingest/

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/ingest-service /usr/local/bin/ingest-service
EXPOSE 8082
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- http://localhost:8082/healthz || exit 1
ENTRYPOINT ["ingest-service"]
