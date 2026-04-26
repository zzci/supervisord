# Notices

`zzci/supervisord` is a fork of [`ochinchina/supervisord`](https://github.com/ochinchina/supervisord), itself a Go re-implementation of the Python supervisord process supervisor. The root [`LICENSE`](LICENSE) (MIT, Steven Ou 2017) covers the original codebase and continues to apply to the fork.

During the fork's cleanup we **inlined** several upstream Go libraries that were no longer maintained, instead of pulling them as `go.mod` dependencies. Their original copyright notices and license terms continue to apply to the inlined source. They are listed below.

| Upstream | Inlined into | License | Copyright |
|---|---|---|---|
| [`github.com/ochinchina/go-ini`](https://github.com/ochinchina/go-ini) | `internal/config/ini*.go` | MIT | (c) 2017 Steven Ou |
| [`github.com/ochinchina/go-daemon`](https://github.com/ochinchina/go-daemon) (descended from sevlyar/go-daemon) | rewritten as `cmd/supervisord/daemonize.go` | MIT | (c) 2013 Sergey Yarmonov |
| [`github.com/ochinchina/go-reaper`](https://github.com/ochinchina/go-reaper) | rewritten as `cmd/supervisord/zombie_reaper.go` | MIT | (c) 2015 ramr |
| [`github.com/ochinchina/filechangemonitor`](https://github.com/ochinchina/filechangemonitor) | `internal/process/fcm_*.go` | Apache-2.0 | (c) ochinchina |
| [`github.com/ochinchina/gorilla-xmlrpc`](https://github.com/ochinchina/gorilla-xmlrpc) (xml subpackage) | `internal/xmlrpc/*.go` | BSD-2-Clause | (c) 2013 Ivan Daniluk |

The full BSD-2-Clause text for the inlined `gorilla-xmlrpc` is preserved at [`internal/xmlrpc/LICENSE`](internal/xmlrpc/LICENSE). The other four upstream licenses (MIT and Apache-2.0) carry no further notice obligation beyond crediting the copyright holders, which this file does.

The `go-daemon` and `go-reaper` rows say "rewritten" because the inline implementations were reduced and adapted to the project's needs (~80 LoC and ~30 LoC respectively); the originals were the design reference. The other three were copied largely verbatim with package renames.

If you redistribute binaries built from this repository, no additional action is required: this `NOTICE.md` plus the per-package `LICENSE` files satisfy attribution.
