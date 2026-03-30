FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o fleet-service ./fleet/service/

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/fleet-service /usr/local/bin/fleet-service
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- http://localhost:8080/healthz || exit 1
ENTRYPOINT ["fleet-service"]
