# dbackup — Docker Container Cold Backup & Cross-Server Restore


---

## Overview

**dbackup** is a single Go binary that backs up *everything a set of containers actually
depends on* and restores it, byte-for-byte, on any other machine.

It scans running (or stopped) containers through the Docker Engine API, reverse-engineers a
complete, directly-usable `docker-compose.yml` from each container's authoritative
`docker inspect` data, and packages that compose file together with all locally-mounted data
— bind mounts, named volumes, and **anonymous volumes** (where most CasaOS apps keep their
data) — into a single tar archive. On the target server, one command pulls the images by tag,
restores the data in place, and brings the services back up with identical configuration.

Images are *not* packaged; they are re-pulled by tag at restore time, so the backup works
across CPU architectures.

**dbackup** 是一个 Go 单二进制工具：把一组容器**真正依赖的全部东西**完整备份下来，并在任意
一台新机器上原样恢复。

它通过 Docker Engine API 扫描运行中（或已停止）的容器，基于每个容器权威、完整的
`docker inspect` 数据逆向生成一份完整、可直接使用的 `docker-compose.yml`，再将该 compose
文件与所有本地挂载数据 —— bind 挂载、命名卷，以及**匿名卷**—— 一起打包为单个 tar 归档。在目标服务器上，一条命令即可按 tag 重新拉取镜像、把数据
落回原路径、并以完全一致的配置拉起全部服务。

镜像不打包，恢复时按 tag 重新拉取，支持跨 CPU 架构。

## Features

- **inspect → compose, lossless.** The compose file is not guessed — it is generated from
  Docker's own authoritative container description, with denormalization of Docker's internal
  canonical forms (mount `rw` suffixes, `depends_on` condition strings, default-bridge
  networks, extra-hosts, volume/network project prefixes, …). A `backup → restore → backup`
  round-trip is stable: names, data, and configuration never degrade.
- **All three mount kinds covered.** Bind mounts, named volumes, and anonymous volumes.
- **Cold backup by default.** Containers are stopped in reverse dependency order, data is
  copied as a consistent snapshot, then everything is restarted in dependency order —
  even if the backup fails part-way. `--live` is available for a faster, best-effort mode.
- **Restore matches the backup-time running state by default.** Services that were stopped
  at backup time stay stopped after restore; `--start-all` explicitly starts everything.
- **Self-describing archive.** Every tar contains the compose file, a `manifest.json` with the
  full original `inspect` of every service, a generated human-readable `README.md`, the config
  used, and the data trees. `dbackup verify` cross-checks the manifest against the actual
  archive contents before you commit to a restore.
- **Scheduled execution.** systemd timer (recommended), cron, or a built-in long-running
  `serve` loop — with `--keep N` retention.


## Repository Layout

```
cmd/
  dbackup/          Program entry point (package main): the CLI shell — cobra command
                    definitions for scan / backup / restore / verify / serve /
                    install-cron / install-timer. Parses flags, calls into internal/
                    packages, prints results. Deliberately thin: no core logic lives here.
internal/           All core logic, one single-responsibility package per concern.
                    (Go's internal/ path means "importable only from within this module"
                    — i.e., implementation details, not a public API.)
  dc/               Hand-rolled Docker Engine API client over the unix socket
                    (~10 endpoints: containers, inspect, stop/start, volumes, info).
                    No docker library dependency — keeps the binary dependency-light and
                    makes format mismatches fail loudly.
  compose/          The inspect → compose generator: field mapping, default-value
                    denoising, denormalization, service-key/project inference.
                    This is the technical heart of the tool.
  backup/           Backup engine: selection → inspect → compose → reverse-topological
                    stop → data planning (dedup, anonymous-volume detection) → streaming
                    tar → forward-topological restart. Plus keep-retention and the
                    generated archive README.
  restore/          Restore engine: stream-read the tar, place bind data at original
                    absolute paths, create volumes under their original names, adjust the
                    compose (name pinning, port overrides), `docker compose pull && up -d`,
                    then match the backup-time running state.
  tarx/             Self-contained tar reader/writer: preserves mode/uid/gid/mtime,
                    symlinks, hardlinks.
  manifest/         Data structures for manifest.json and data/index.json.
  config/           Optional config.yaml parsing (include/exclude, per-container
                    overrides, restore-side compose overrides).
go.mod / go.sum     Module definition and pinned dependencies (cobra + yaml.v3).
```

- **构建产物**：`go build ./cmd/dbackup` 产出 `dbackup` 单二进制；备份时只需 docker socket
  访问权，恢复时宿主机需有 `docker compose`（v2 插件）。

- **目录结构**

```
cmd/
  dbackup/          程序入口（package main）：CLI 外壳 —— scan / backup / restore /
                    verify / serve / install-cron / install-timer 的 cobra 命令定义。
                    负责解析参数、调用 internal/ 各包、打印结果。刻意保持薄：核心逻辑
                    不放在这里。
internal/           全部核心逻辑，按单一职责拆包。（Go 的 internal/ 路径语义 =
                    "只允许本模块内导入"，即实现细节而非对外 API。）
  dc/               手写的 Docker Engine API 客户端（unix socket，约 10 个端点：
                    containers/inspect/stop/start/volumes/info）。不依赖 docker 官方库，
                    保持二进制依赖极轻，且格式不一致会显式报错。
  compose/          inspect → compose 生成器：字段映射、默认值去噪、反规范化、
                    service key 与 project 推断。本工具的技术核心。
  backup/           备份引擎：选集 → inspect → compose → 逆拓扑停容器 → 数据规划
                    （去重、匿名卷识别）→ 流式打 tar → 正拓扑拉起。含 keep 保留策略与
                    归档 README 生成。
  restore/          恢复引擎：流式读 tar → bind 数据落回原绝对路径 → 按原始卷名建卷
                    → 调整 compose（固定卷/网络名、端口覆盖）→ `docker compose pull &&
                    up -d` → 按备份时运行状态收尾。
  tarx/             自研 tar 读写：保留 mode/uid/gid/mtime、符号链接、硬链接。
  manifest/         manifest.json 与 data/index.json 的数据结构。
  config/           可选 config.yaml 解析（include/exclude、逐容器覆盖、恢复侧
                    compose 覆盖）。
go.mod / go.sum     模块定义与依赖锁定（仅 cobra + yaml.v3）。
```

## Building

Requires Go 1.26+. No CGO, no runtime dependencies.

```bash
go build -o dbackup ./cmd/dbackup
# static, stripped:
CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o dbackup ./cmd/dbackup
```

构建需 Go 1.26+。无 CGO、无运行时依赖。

```bash
go build -o dbackup ./cmd/dbackup
# 静态、去符号：
CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o dbackup ./cmd/dbackup
```

## Quick Start

```bash
# 1. See what would be backed up (read-only: containers, mounts, compose preview)
dbackup scan

# 2. Run a cold backup (stop → copy → restart automatically)
dbackup backup -o /backups --name myserver

# 3. Verify the archive is complete and self-consistent
dbackup verify --tar /backups/myserver-<timestamp>.tar --strict

# 4. Restore on any other server (re-pulls images, places data, starts services)
dbackup restore --tar myserver-<timestamp>.tar
```

```bash
# 1. 预览备份范围（只读：容器、挂载、compose 预览）
dbackup scan

# 2. 执行冷备份（自动 停 → 拷 → 拉起）
dbackup backup -o /backups --name myserver

# 3. 校验备份包完整自洽
dbackup verify --tar /backups/myserver-<时间戳>.tar --strict

# 4. 在任意其他服务器恢复（重拉镜像、落数据、拉起服务）
dbackup restore --tar myserver-<时间戳>.tar
```

## Commands

| Command | Purpose |
|---|---|
| `scan` | Read-only inventory of containers/mounts + generated compose preview |
| `backup` | Run a backup (cold by default; `--live`, `--keep`, `--dry-run`, …) |
| `restore` | Restore from a tar (`--dry-run`, `--no-pull`, `--data-dir`, `--start-all`, …) |
| `verify` | Reconcile manifest vs. actual archive contents (`--strict` exits non-zero on diff) |
| `serve` | Long-running scheduled backups for environments without cron/systemd |
| `install-cron` | Install a crontab line |
| `install-timer` | Install a systemd service + timer (logs to the journal) |

Restore semantics: by default the restored running state **matches the backup-time state**
per service; add `--start-all` to bring every service up. Data is always restored either way
— stopping a service is state control, not data handling.

| 命令 | 用途 |
|---|---|
| `scan` | 只读盘点容器/挂载 + 生成 compose 预览 |
| `backup` | 执行备份（默认冷备份；`--live`、`--keep`、`--dry-run` 等） |
| `restore` | 从 tar 恢复（`--dry-run`、`--no-pull`、`--data-dir`、`--start-all` 等） |
| `verify` | manifest 声明与归档实际内容对账（`--strict` 有差异时非零退出） |
| `serve` | 无 cron/systemd 环境的常驻定时备份 |
| `install-cron` | 安装 crontab 行 |
| `install-timer` | 安装 systemd service + timer（日志进 journal） |

恢复语义：默认恢复后的运行状态**与备份时逐服务一致**；加 `--start-all` 才全部拉起。
数据无论如何都会完整恢复 —— 停止服务只是状态控制，不影响数据落盘。

## Configuration (optional)

`config.yaml`, passed with `-c` to backup/restore:

```yaml
include: all            # or [web, db]
exclude: [some-tool]
keep: 5                 # retain the 5 most recent archives
name: myserver          # archive name prefix
containers:
  web:
    depends_on: [db]        # dependencies inspect cannot recover
    skip_mounts: [/scratch] # mount target to skip
    extra_data: [/opt/x]    # extra host directory to include
    restore_compose:        # restore-side overrides only
      ports: ["18080:80"]
```

`config.yaml`（可选，`-c` 传给 backup/restore）：

```yaml
include: all            # 或 [web, db] 指定容器名
exclude: [some-tool]
keep: 5                 # 只保留最近 5 份
name: myserver          # 归档名前缀
containers:
  web:
    depends_on: [db]        # inspect 无法还原的依赖
    skip_mounts: [/scratch] # 跳过的挂载目标
    extra_data: [/opt/x]    # 额外纳入的宿主目录
    restore_compose:        # 仅恢复侧生效的覆盖
      ports: ["18080:80"]
```

## Archive Layout

```
<name>-<YYYYMMDD-HHMMSS>-<host>.tar
└── <name>-<timestamp>/
    ├── docker-compose.yml   complete, directly usable compose
    ├── manifest.json        per-service full original inspect + mount→data map + warnings
    ├── README.md            auto-generated restore instructions
    ├── config.yaml          the configuration used for this backup
    └── data/<entry>/…       one deduplicated tree per unique mount source,
                             preserving mode/uid/gid/mtime, symlinks, hardlinks
```

```
<name>-<YYYYMMDD-HHMMSS>-<主机名>.tar
└── <name>-<时间戳>/
    ├── docker-compose.yml   完整、可直接使用的 compose
    ├── manifest.json        每服务完整原始 inspect + 挂载→数据条目映射 + 警告
    ├── README.md            自动生成的恢复说明
    ├── config.yaml          本次备份所用的配置
    └── data/<条目>/…        每个唯一挂载源一份去重后的目录树，
                             保留 mode/uid/gid/mtime、符号链接、硬链接
```

## Design Notes

- **Images are re-pulled by tag**, not shipped — the restore host must reach an image
  registry (or pre-stage images and use `--no-pull`).
- **Warnings are recorded, not fixed:** digest-pinned images, `network_mode: host`, and host
  devices are listed in `manifest.json` and the archive README for manual review.
- **Cold vs. live:** cold backup is the recommended default (consistency window); `--live`
  does not stop containers and makes no consistency guarantees.

- **镜像按 tag 重拉**而非打包 —— 恢复机需能访问镜像源（或提前备好镜像并用 `--no-pull`）。
- **警告记录而非修复**：digest 锁定的镜像、`network_mode: host`、宿主设备，会写入
  `manifest.json` 与归档 README，供人工复核。
- **冷 vs live**：冷备份是推荐的默认路径（一致性窗口）；`--live` 不停容器、不做一致性保证。
