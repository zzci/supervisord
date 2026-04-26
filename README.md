# supervisord

[English](README.md) · [中文](README.zh-CN.md)

A Go process supervisor designed for containers. Forked from
[`ochinchina/supervisord`](https://github.com/ochinchina/supervisord) (itself a
Go re-implementation of Python's supervisord) and stripped down to a small,
maintainable core plus container-friendly features.

Used as the in-container init system for [`zzci/init`](https://github.com/zzci/init).

## What's different from upstream

- Module path: `github.com/zzci/supervisord`.
- Single Go module, all source under `cmd/` and `internal/`.
- All five unmaintained `ochinchina/*` libraries inlined; zero stale deps. See [`NOTICE.md`](NOTICE.md).
- HTTP transport restricted to **UNIX socket only**. The `[inet_http_server]` section is parsed but ignored with a warning. Filesystem permissions on the socket are the trust boundary.
- BasicAuth removed. `[unix_http_server].username` / `password` are ignored with a warning.
- Web GUI removed.
- New: `depends_on` is a real readiness gate (waits for dependency to enter Running, not just spawn order).
- New: HTTP/TCP/file/command healthchecks per program, with `depends_on_ready=healthy` to wait on Healthy instead of Running.
- Strict CI: gofmt, golangci-lint v2, go vet, govulncheck, gosec, race-enabled tests. See [`.github/workflows/ci.yml`](.github/workflows/ci.yml).

## Build

Toolchain: Go 1.25 (auto-downloaded via `go.mod`'s `toolchain` directive).

```bash
# Build into ./dist
task build

# Static linux binary for FROM scratch
task build:static

# Or directly
go build -o dist/supervisord ./cmd/supervisord
```

Container image:

```bash
task docker     # docker build -t zzci/supervisord:dev .
```

### Pre-built release binaries

Tagged releases publish four tarballs (linux/darwin × amd64/arm64) plus a
`checksums.txt`. Tarballs are flat — extracting yields `supervisord`,
`LICENSE`, and `NOTICE.md` at the top level. Downstream images can grab
the binary directly:

```dockerfile
ARG SUPERVISORD_VER=v0.1.0
ARG TARGETARCH
RUN ARCH=$([ "$TARGETARCH" = "arm64" ] && echo "arm64" || echo "x86_64") && \
    curl -fsSL "https://github.com/zzci/supervisord/releases/download/${SUPERVISORD_VER}/supervisord_${SUPERVISORD_VER#v}_Linux_${ARCH}.tar.gz" \
      | tar -xz -C /usr/local/bin/ supervisord
```

Binaries are statically linked (`CGO_ENABLED=0`, `-extldflags -static`),
trimmed (`-trimpath -s -w`), no UPX. Suitable for `FROM scratch`.

## Configure

Drop-in compatible with Python supervisord's INI format for the parts that
remain. A fully annotated reference covering every section and every
per-program key lives at [`example/supervisord.conf`](example/supervisord.conf);
see [`example/README.md`](example/README.md) for a quick index.

Minimal example:

```ini
[unix_http_server]
file = /var/run/supervisor.sock

[supervisord]
logfile = /var/log/supervisord.log
pidfile = /var/run/supervisord.pid

[supervisorctl]
serverurl = unix:///var/run/supervisor.sock

[program:web]
command = /usr/bin/myserver
autostart = true
autorestart = true
```

### Dependency gating + healthchecks

```ini
[program:db]
command = /usr/bin/postgres
healthcheck_http = http://127.0.0.1:5432/
healthcheck_retries = 5

[program:web]
command = /usr/bin/myserver
depends_on = db
depends_on_ready = healthy        ; wait until db's healthcheck passes
depends_on_timeout = 120
depends_on_strategy = abort       ; or 'ignore'
```

`healthcheck_http`, `healthcheck_tcp`, `healthcheck_file`, and
`healthcheck_command` are all optional. If any are configured, **all must
succeed** for an attempt to count.

| Field | Success when |
|---|---|
| `healthcheck_http = url` | HTTP GET returns 2xx |
| `healthcheck_tcp = host:port` | TCP `connect()` succeeds |
| `healthcheck_file = path` | `os.Stat(path)` succeeds |
| `healthcheck_command = cmdline` | `/bin/sh -c <cmdline>` exits 0 |

UDP probes are not natively supported; UDP is connectionless, so a generic
"is the server up" check would lie. Use `healthcheck_command` with a
protocol-specific probe if you need one.

Knobs: `healthcheck_timeout` (5s), `healthcheck_interval` (2s),
`healthcheck_retries` (3 consecutive successes required to flip Healthy).

## Use

```bash
# Run the daemon
supervisord -c /etc/supervisord.conf

# Run as a daemon (re-execs detached, writes supervisord.pid)
supervisord -d -c /etc/supervisord.conf

# Same binary, supervisorctl mode
supervisord ctl status
supervisord ctl start <name>
supervisord ctl stop  <name>
supervisord ctl restart <name>
supervisord ctl signal <SIG> <name>
supervisord ctl pid <name>
supervisord ctl logtail <name> stdout
supervisord ctl reload
supervisord ctl shutdown
```

## HTTP surface

When the unix socket is configured, the daemon serves:

| Path | Method | Purpose |
|---|---|---|
| `/RPC2` | POST | XML-RPC endpoint; what `supervisord ctl` (and any supervisorctl-compatible client) talks to |
| `/logtail/{program}/stdout` | GET | streamed stdout log for the program (chunked) |
| `/logtail/{program}/stderr` | GET | streamed stderr log for the program (chunked) |
| `/log/{program}/` | GET | static file server rooted at the program's stdout log directory |
| `/metrics` | GET | Prometheus metrics for the daemon and supervised processes |

`{program}` is the program name as defined in supervisor.conf (`[program:foo]` -> `foo`).

If you need network access to any of this, bridge the socket externally
(e.g. `socat TCP-LISTEN:9001,reuseaddr,fork UNIX-CONNECT:/var/run/supervisor.sock`)
and add CSP/auth at that layer.

## Tests

```bash
task test           # unit tests, race-enabled, fast
task test:e2e       # end-to-end on host (spawns real supervisord)
task test:e2e:docker  # same e2e suite inside an ephemeral container
```

`task test:e2e:docker` is the recommended way to iterate locally — every test
process and socket lives inside the container and is removed on exit.

## Notices

Inlined upstream attributions: [`NOTICE.md`](NOTICE.md).

## License

MIT, see [`LICENSE`](LICENSE) and [`NOTICE.md`](NOTICE.md).
