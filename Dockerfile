FROM golang:1.26-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o /webhook .

FROM gcr.io/distroless/static-debian12

WORKDIR /

COPY --from=builder /webhook /webhook
COPY certs/server.cert.pem /etc/webhook/certs/server.cert.pem
COPY certs/server.key.pem /etc/webhook/certs/server.key.pem

EXPOSE 8443

ENTRYPOINT ["/webhook"]