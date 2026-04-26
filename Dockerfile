FROM golang:alpine AS builder

RUN apk add --no-cache --update git gcc musl-dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -extldflags -static" \
    -o /out/supervisord ./cmd/supervisord

FROM scratch

COPY --from=builder /out/supervisord /usr/local/bin/supervisord

ENTRYPOINT ["/usr/local/bin/supervisord"]
