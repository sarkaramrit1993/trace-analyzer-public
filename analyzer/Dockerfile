FROM golang:1.21-alpine AS builder

RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=1 GOOS=linux go build -o /analyzer ./cmd/analyzer

FROM alpine:3.24

RUN apk add --no-cache sqlite-libs ca-certificates

WORKDIR /app

COPY --from=builder /analyzer .

RUN mkdir -p /app/data

ENV DATA_PATH=/app/data
ENV API_PORT=8080

EXPOSE 8080

CMD ["./analyzer"]
