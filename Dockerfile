# Build stage
FROM golang:1.26.5-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /gateway ./cmd/api
RUN CGO_ENABLED=0 GOOS=linux go build -o /seed ./scripts

# Final stage — minimal runtime image, no Go toolchain baked in
FROM alpine:latest

WORKDIR /root/

COPY --from=builder /gateway .
COPY --from=builder /seed .
COPY migrations ./migrations

EXPOSE 8080

CMD ["./gateway"]
