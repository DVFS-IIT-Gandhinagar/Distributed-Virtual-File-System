FROM golang:1.24-alpine AS builder

RUN apk add --no-cache make git

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN make build

FROM alpine:latest

RUN apk add --no-cache make libc6-compat

WORKDIR /app

COPY --from=builder /app/bin /app/bin
COPY --from=builder /app/Makefile /app/Makefile

RUN mkdir -p /app/fileserver_data

EXPOSE 50051

CMD ["sh"]
