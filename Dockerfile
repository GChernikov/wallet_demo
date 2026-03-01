FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /balance-api ./cmd/balance-api

FROM alpine:3.21

RUN apk --no-cache add ca-certificates tzdata

COPY --from=builder /balance-api /balance-api

EXPOSE 8080

ENTRYPOINT ["/balance-api"]
