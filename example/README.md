# Example configuration

[`supervisord.conf`](supervisord.conf) is a single annotated reference
configuration covering every section and every per-program key the daemon
honours.

## How to use

The file is **not** meant to be copy-pasted as-is — it documents every
option so you can pick the ones you need. A real deployment usually only
sets a few keys per program.

The sections are arranged so the file reads top-to-bottom:

1. **`[unix_http_server]`** — XML-RPC listener. The only listener used.
2. **`[supervisord]`** — daemon-wide settings (logs, pidfile, rlimits).
3. **`[supervisorctl]`** — defaults for the bundled `supervisord ctl`
   client.
4. **`[include]`** — optional glob include for fragment files.
5. **`[program:reference]`** — one program section that names every
   recognised key, each with an inline comment.
6. **Realistic snippets** — `web`, `db`, `worker`, `nightly-cleanup`,
   `stack` group, and an event listener template for copy/paste.

## Key reference (quick index)

### Daemon

| Section | Notable keys |
|---|---|
| `[unix_http_server]` | `file` |
| `[supervisord]` | `logfile`, `logfile_maxbytes`, `logfile_backups`, `loglevel`, `pidfile`, `identifier`, `minfds`, `minprocs` |
| `[supervisorctl]` | `serverurl` |
| `[include]` | `files` (glob) |

### Program lifecycle

`command`, `process_name`, `numprocs`, `numprocs_start`, `priority`,
`autostart`, `autorestart`, `startsecs`, `startretries`, `restartpause`,
`exitcodes`, `stopsignal`, `stopwaitsecs`, `killwaitsecs`, `stopasgroup`,
`killasgroup`, `user`, `directory`, `environment`, `envFiles`,
`redirect_stderr`.

### Logging

`stdout_logfile`, `stdout_logfile_maxbytes`, `stdout_logfile_backups`,
`stdout_capture_maxbytes`, `stdout_events_enabled`, mirror four for
`stderr_*`. Plus `syslog_facility`, `syslog_tag`,
`syslog_stdout_priority`, `syslog_stderr_priority`.

### Scheduling

`cron` (six-field expression with seconds).

### Restart on change

`restart_when_binary_changed`, `restart_cmd_when_binary_changed`,
`restart_signal_when_binary_changed`, `restart_directory_monitor`,
`restart_file_pattern`, `restart_cmd_when_file_changed`,
`restart_signal_when_file_changed`.

### Dependency gating (zzci-fork specific)

| Key | Default | Behaviour |
|---|---|---|
| `depends_on` | — | comma-separated dependency program names |
| `depends_on_timeout` | `60` | seconds before the strategy kicks in |
| `depends_on_strategy` | `abort` | `abort` marks Fatal, `ignore` warns and starts |
| `depends_on_ready` | `running` | `running` waits for state, `healthy` waits for healthcheck |

### Healthchecks (zzci-fork specific)

| Key | Success when |
|---|---|
| `healthcheck_http` | HTTP GET returns 2xx |
| `healthcheck_tcp` | TCP `connect()` succeeds |
| `healthcheck_file` | `os.Stat(path)` succeeds |
| `healthcheck_command` | `/bin/sh -c <cmd>` exits 0 |

Knobs: `healthcheck_timeout` (5s), `healthcheck_interval` (2s),
`healthcheck_retries` (3), `healthcheck_startup_timeout` (300s, ceiling
on the wait for the program to reach Running before the loop starts).

If any field is set, **all configured checks must pass per attempt**.

### Group

`[group:name]` with `programs = a,b,c` — start/stop/signal as a unit.

### Event listener

`[eventlistener:name]` accepts the same keys as `[program:*]` plus
`events` (comma-separated event types) and `buffer_size`.

## Removed vs. upstream

The fork drops these on purpose; the daemon either ignores them with a
warning or never offers them:

- `[inet_http_server]` listener (parsed, ignored)
- `[unix_http_server].username` / `password` (parsed, ignored)
- Web GUI under `/`, REST API under `/program/...`, `/supervisor/...`,
  `/conf/{program}`
- `supervisord init` template-emitter subcommand
- `pidproxy` helper binary

See [`../NOTICE.md`](../NOTICE.md) for inlined upstream attribution and
the README for the full picture.
