# Ephemeral test image — runs the e2e suite end-to-end inside a container so
# the developer's host stays clean (no stray sockets, pidfiles, or sleeper
# processes). Used by `task test:e2e:docker`.
#
# Not pushed; built and removed on every invocation.

FROM golang:1.26.2-alpine

RUN apk add --no-cache --update git

WORKDIR /src

# Cache module downloads in a separate layer so iterating on test code is fast.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ENV CGO_ENABLED=0

# Default command: run the e2e suite. Tests build cmd/supervisord themselves
# via TestMain, so no separate build step is needed here.
CMD ["go", "test", "-tags", "e2e", "-count=1", "-timeout", "5m", "-v", "./tests/e2e/..."]
