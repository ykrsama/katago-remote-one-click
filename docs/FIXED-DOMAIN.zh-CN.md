# 固定域名（使用自己的 Cloudflare 域名）

默认的 `trycloudflare.com` 是 Cloudflare Quick Tunnel 临时域名，服务或服务器重启后通常会变化。如果域名（例如 `ykrsama.org`）已经托管在 Cloudflare，可以改用**命名隧道**，把地址固定成 `wss://katago.ykrsama.org/katago/<私密令牌>`。

> 命名隧道从 v0.1.1 起提供。v0.1.0 的 release 二进制只支持临时域名；如果服务器上还是旧版本，先按 2a 升级。

## 整体链路

```text
LizzieYzy Next
    |  wss://katago.ykrsama.org/katago/<私密令牌>   TLS 由 Cloudflare 终止
    v
Cloudflare 命名隧道（固定域名）
    |  加密隧道，由 GPU 服务器主动向外建立，不需要开放入站端口
    v
生产服务器上的 cloudflared
    |  http://127.0.0.1:8765
    v
katago-remote bridge -> KataGo
```

关键点：

- `cloudflared` 必须运行在 **GPU 服务器（生产环境）** 上。域名固定在 Cloudflare，但真正提供算力的是那台服务器。
- 本机开发环境不需要、也不要运行同一个 token。同一个隧道同时有多个连接器时，Cloudflare 会在它们之间分流，链接会时通时不通。
- 固定域名不等于公开：路径里仍然有 256 位随机令牌，Cloudflare 只是替你转发。

## 步骤 1：Cloudflare 侧（一次性）

1. 打开 [Zero Trust 控制台](https://one.dash.cloudflare.com/) → **Networks → Tunnels** → **Create a tunnel**。
2. 连接器类型选 **Cloudflared**，名称填 `katago-prod`，保存后复制 **token**（`eyJ` 开头的一长串）。
3. 在该隧道的 **Public Hostname** 中新增一条：
   - **Subdomain**：`katago`
   - **Domain**：`ykrsama.org`
   - **Service**：`http://127.0.0.1:8765`
4. 保存。Cloudflare 会自动创建 `katago.ykrsama.org` 的 CNAME 记录，不需要手动添加 DNS。

**Service 必须和服务的监听地址一致**（默认 `127.0.0.1:8765`）。写成 `https://`、写成公网 IP、或端口不一致，固定域名都会返回 1033 或 502。

## 步骤 2：生产服务器

### 2a. 已经装过（v0.1.0 升级并切换）

先确认版本：

```bash
~/.local/bin/katago-remote version   # 需要 v0.1.4 或更新
```

还是 `v0.1.0` 时，最省事的是带上隧道变量重跑安装器。它只会重新下载哈希已变化的二进制，KataGo、权重和配置会复用，并在最后写入命名隧道配置：

```bash
curl -fsSLo /tmp/katago-remote-install.sh \
  https://raw.githubusercontent.com/ykrsama/katago-remote-one-click/v0.1.4/install.sh

KATAGO_REMOTE_TUNNEL_HOSTNAME=katago.ykrsama.org \
KATAGO_REMOTE_TUNNEL_TOKEN='eyJ...' \
bash /tmp/katago-remote-install.sh
```

如果不想重跑安装器，也可以按 2c 手动替换二进制，然后执行下面的命令：

```bash
umask 077
printf '%s\n' 'eyJ...你的tunnel-token...' > /root/katago-tunnel-token

~/.local/bin/katago-remote init \
  --tunnel-mode named \
  --tunnel-hostname katago.ykrsama.org \
  --tunnel-token-file /root/katago-tunnel-token

~/.local/bin/katago-remote restart
```

`init` 只覆盖你明确传入的字段，不会重置已有的 KataGo、模型、配置、监听地址和 `maxVisits`。

token 会被复制到 `~/.local/share/katago-remote-one-click/state/tunnel-token`（权限 `600`），并通过 `TUNNEL_TOKEN` 环境变量交给 cloudflared，因此不会出现在进程参数里。`/root/katago-tunnel-token` 只是 `init` 的输入，之后可以删除。

### 2b. 新装服务器

在 v0.1.4 及之后，把变量传给 **Bash**（而不是 `curl`）：

```bash
curl -fsSLo /tmp/katago-remote-install.sh \
  https://raw.githubusercontent.com/ykrsama/katago-remote-one-click/v0.1.4/install.sh

KATAGO_REMOTE_TUNNEL_HOSTNAME=katago.ykrsama.org \
KATAGO_REMOTE_TUNNEL_TOKEN='eyJ...' \
bash /tmp/katago-remote-install.sh
```

如果不想把 token 写进 shell 历史，改用 `KATAGO_REMOTE_TUNNEL_TOKEN_FILE=/root/katago-tunnel-token`。

`curl ... | bash` 的写法里，变量只会传给 `curl`，Bash 看不到，所以必须像上面这样先下载脚本。

### 2c. 用源码构建二进制（可选）

只在你想手动替换二进制、或需要自行验证源码时使用。任意一台有 Go 的机器都可以，包括本机开发环境：

```bash
git clone https://github.com/ykrsama/katago-remote-one-click
cd katago-remote-one-click
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
  -trimpath -buildvcs=false \
  -ldflags "-s -w -X main.version=v0.1.4" \
  -o katago-remote-linux-amd64 ./cmd/katago-remote
```

把二进制复制到服务器，替换服务程序后执行：

```bash
install -m 700 katago-remote-linux-amd64 \
  ~/.local/share/katago-remote-one-click/bin/katago-remote
~/.local/bin/katago-remote restart
```

注意：`~/.local/bin/katago-remote` 是指向上面这个文件的符号链接，只需要替换目标文件。

## 步骤 3：验证

```bash
katago-remote doctor
katago-remote status
katago-remote show
```

`doctor` 在命名隧道模式下会多出三项：`Tunnel hostname`、`Tunnel token`、`Tunnel ingress`。

用浏览器打开 `https://katago.ykrsama.org/`：

- 返回 404 或 401：隧道已打通，只是没带私密令牌，正常。
- 返回 1033：隧道没有在线连接器，检查服务器上的 cloudflared 是否在运行。
- 返回 502：Cloudflare 找不到源站，检查 Public Hostname 的 Service 是否等于 `http://127.0.0.1:8765`。

浏览器测试不能代替真正的 WebSocket 验证，最终要在 LizzieYzy Next 里粘贴完整链接测试。

## 日常维护

| 需求 | 操作 |
| --- | --- |
| 域名不变，只换私密链接 | `katago-remote reset-link` |
| 轮换 Cloudflare token | 控制台重新生成 token → 更新 `/root/katago-tunnel-token` → 重新执行 2a |
| 换主机名 | 控制台改 Public Hostname，然后 `init --tunnel-hostname 新域名` + `restart` |
| 停止服务 | `katago-remote stop`（域名仍然解析，但返回 1033） |
| 回到临时域名 | `katago-remote init --tunnel-mode quick` + `restart` |

## 常见误区

- **在开发机上跑 cloudflared**：域名会指向开发机，而不是生产 GPU 服务器。
- **同一个 token 跑两份**：Cloudflare 会在多个连接器之间分流，表现为链接时通时不通。
- **Public Hostname 的 Service 写错**：写成 `https://`、公网 IP、或端口不是 8765，都会 502。
- **以为固定域名就不用令牌**：链接里的私密令牌仍是唯一的访问控制，泄露后立即 `reset-link`。
