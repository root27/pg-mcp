FROM golang:1.23-alpine AS builder

WORKDIR /app

COPY . .

RUN go mod download

RUN  go build -o pg-mcp .


FROM scratch

WORKDIR /

COPY --from=builder /app/pg-mcp /pg-mcp

ENTRYPOINT ["./pg-mcp"]

