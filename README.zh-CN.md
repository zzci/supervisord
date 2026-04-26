# supervisord

[English](README.md) · [中文](README.zh-CN.md)

为容器场景打造的 Go 进程守护工具。Fork 自
[`ochinchina/supervisord`](https://github.com/ochinchina/supervisord)（其本身是
Python supervisord 的 Go 实现），裁剪到一个小而易维护的内核，并补足容器
友好的能力。

作为 [`zzci/init`](https://github.com/zzci/init) 容器镜像里 PID 1 init
使用。

## 与上游的差异

- 模块路径：`github.com/zzci/supervisord`。
- 单一 Go module，所有源码在 `cmd/` 与 `internal/`。
- 全部 5 个停更的 `ochinchina/*` 库已 inline 入仓库；不再有上游依赖漂移
  问题，归属信息见 [`NOTICE.md`](NOTICE.md)。
- HTTP 仅监听 **UNIX socket**。`[inet_http_server]` 段会被解析但被忽略并
  打 warning。访问控制完全交给 socket 文件的 POSIX 权限。
- 删除 BasicAuth；`[unix_http_server].username` / `password` 被忽略并打
  warning。
- 删除 Web GUI 以及它背后的 REST/confApi handler；XML-RPC 是唯一的协议
  契约。
- `depends_on` 是真正的就绪门：`depends_on_timeout`、`depends_on_strategy`
  (`abort` | `ignore`)、`depends_on_ready` (`running` | `healthy`)。配置
  加载阶段就检测循环依赖与悬空引用。
- 每个 program 可配 healthcheck：`healthcheck_http`、`healthcheck_tcp`、
  `healthcheck_file`、`healthcheck_command`，附 `_timeout` / `_interval`
  / `_retries` 调节。
- 严格 CI：gofmt、golangci-lint v2、`go vet`、govulncheck、gosec
  (`-severity medium`)、`go test -race`,详见
  [`.github/workflows/ci.yml`](.github/workflows/ci.yml)。

## 编译

工具链：Go 1.25（`go.mod` 的 `toolchain` 指令会按需自动下载）。

```bash
# 二进制输出到 ./dist
task build

# FROM scratch 容器用的全静态 linux 二进制
task build:static

# 直接命令也行
go build -o dist/supervisord ./cmd/supervisord
```

容器镜像：

```bash
task docker     # docker build -t zzci/supervisord:dev .
```

### 直接使用预编译版本

每个 tag 会发布 4 个 tarball（linux/darwin × amd64/arm64）和
`checksums.txt`。tarball 是扁平的——解压后顶层只有 `supervisord`、
`LICENSE`、`NOTICE.md`。下游镜像可以直接抓二进制：

```dockerfile
ARG SUPERVISORD_VER=v0.1.0
ARG TARGETARCH
RUN ARCH=$([ "$TARGETARCH" = "arm64" ] && echo "arm64" || echo "x86_64") && \
    curl -fsSL "https://github.com/zzci/supervisord/releases/download/${SUPERVISORD_VER}/supervisord_${SUPERVISORD_VER#v}_Linux_${ARCH}.tar.gz" \
      | tar -xz -C /usr/local/bin/ supervisord
```

二进制是静态链接的（`CGO_ENABLED=0`、`-extldflags -static`），strip 过
（`-trimpath -s -w`），不走 UPX。可以直接放入 `FROM scratch`。

## 配置

兼容 Python supervisord INI 格式（仅保留下来的部分）。完整参考配置（覆盖
所有 section 和每个 program 字段，含逐字段注释）见
[`example/supervisord.conf`](example/supervisord.conf)；快速索引见
[`example/README.md`](example/README.md)。

最小化配置：

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

### 依赖启动 + Healthcheck

```ini
[program:db]
command = /usr/bin/postgres
healthcheck_tcp = 127.0.0.1:5432
healthcheck_retries = 5

[program:web]
command = /usr/bin/myserver
depends_on = db
depends_on_ready = healthy        ; 等 db 的 healthcheck 通过
depends_on_timeout = 120
depends_on_strategy = abort       ; 或 'ignore'
```

`healthcheck_http`、`healthcheck_tcp`、`healthcheck_file`、
`healthcheck_command` 全部可选。**配置了几个，每次探活就要全部成功才算
一次"成功"。**

| 字段 | 成功条件 |
|---|---|
| `healthcheck_http = url` | HTTP GET 返回 2xx |
| `healthcheck_tcp = host:port` | TCP `connect()` 成功 |
| `healthcheck_file = path` | `os.Stat(path)` 成功 |
| `healthcheck_command = cmdline` | `/bin/sh -c <cmdline>` 退出码 0 |

不内置 UDP 探活 —— UDP 无连接，泛化的"是否在线"判断会撒谎；如果需要
请用 `healthcheck_command` 跑协议特定的探针。

调节项：`healthcheck_timeout`（5 秒）、`healthcheck_interval`（2 秒）、
`healthcheck_retries`（连续 3 次成功才视为 Healthy）。

## 使用

```bash
# 前台跑
supervisord -c /etc/supervisord.conf

# 守护态跑（自我 re-exec 脱离终端，写 supervisord.pid）
supervisord -d -c /etc/supervisord.conf

# 同一个二进制，作为 supervisorctl 客户端
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

## 测试

```bash
task test            # 单元测试，启用 race，快
task test:e2e        # 主机里跑端到端（spawn 真实 supervisord 子进程）
task test:e2e:docker # 同样的 e2e suite，但跑在临时容器里，主机零副作用
```

本地迭代推荐 `task test:e2e:docker`——所有 spawn 的进程和 socket 都在
容器内，退出即清。

## HTTP 路由

unix socket 配上后，daemon 提供以下路由：

| 路径 | 方法 | 用途 |
|---|---|---|
| `/RPC2` | POST | XML-RPC 端点；`supervisord ctl` 以及任何 supervisorctl 兼容客户端走这里 |
| `/logtail/{program}/stdout` | GET | 程序 stdout 日志的 chunked 流 |
| `/logtail/{program}/stderr` | GET | 程序 stderr 日志的 chunked 流 |
| `/log/{program}/` | GET | 把 program 的 stdout 日志目录当 static file server 暴露 |
| `/metrics` | GET | daemon 与监督进程的 Prometheus 指标 |

`{program}` 即 supervisor.conf 里 `[program:foo]` 的 `foo`。

如果需要从外部访问以上任意端点，请在 socket 之外做桥接（例如
`socat TCP-LISTEN:9001,reuseaddr,fork UNIX-CONNECT:/var/run/supervisor.sock`），
并在桥接层做 CSP / auth。

## Notices

Inline 上游归属：[`NOTICE.md`](NOTICE.md)。

## 协议

MIT。详见 [`LICENSE`](LICENSE) 与 [`NOTICE.md`](NOTICE.md)。
