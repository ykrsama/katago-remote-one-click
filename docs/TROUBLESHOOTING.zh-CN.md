# 故障排查

先运行下面三条命令：

```bash
katago-remote doctor
katago-remote status
katago-remote logs
```

如果提示找不到命令，把 `katago-remote` 换成 `~/.local/bin/katago-remote`。

## 提示找不到 `nvidia-smi`

服务器系统镜像没有可用的 NVIDIA 驱动。最省事的办法是重新选择云厂商提供的 NVIDIA/CUDA 镜像；在普通容器中临时安装驱动通常更慢，也更容易失败。

## KataGo 提示缺少 CUDA 或 cuDNN

优先选择带 CUDA 12 和 cuDNN 9 的镜像。安装器会先试官方 cuDNN 9.8 引擎，再试官方 cuDNN 8.9.7 兼容引擎；它不会静默安装或替换服务器的系统显卡库。

用下面命令保存完整诊断：

```bash
katago-remote doctor 2>&1 | tee katago-remote-doctor.txt
```

公开求助时不要附上完整 WSS 链接。

## 重启以后原链接不能用了

域名来自 Cloudflare 临时通道，服务或服务器重启后通常会变化。重新运行：

```bash
katago-remote start
katago-remote show
```

把新显示的链接粘贴到软件中。使用固定域名（命名隧道）时地址不会变化。

## 固定域名打不开（1033 / 502 / 404）

先确认 Cloudflare 隧道 **Public Hostname** 里的 Service 是 `http://127.0.0.1:8765`，并且和 `config.json` 里的 `listen` 完全一致。

浏览器访问 `https://katago.ykrsama.org/`：

- **404 / 401**：隧道已经打通，只是没带 `/katago/<私密令牌>`，正常。
- **1033**：隧道没有在线连接器。运行 `katago-remote status` 和 `katago-remote logs`，确认 cloudflared 在运行、token 有效。
- **502**：Cloudflare 连不上源站。检查 Public Hostname 的 Service 端口，以及 `katago-remote show` 是否正常。
- **DNS 解析失败**：确认 Public Hostname 已保存，`katago.ykrsama.org` 的 CNAME 指向 `<隧道ID>.cfargotunnel.com`。

同一个 tunnel token 不要在多台机器上同时运行，否则请求会在多个连接器之间分流，表现为时通时不通。详见[固定域名配置](FIXED-DOMAIN.zh-CN.md)。

## 固定域名下链接变了

命名隧道的域名是固定的，只有私密令牌会变。`restart` 不换域名；`reset-link` 只换令牌、域名不变。如果域名本身变了，检查 `config.json` 的 `tunnelHostname`，或重新执行 `katago-remote init --tunnel-hostname 新域名`。

## 软件立即提示连接失败

1. 确认复制了完整链接，包括 `wss://`、`/katago/` 和最后的私密令牌。
2. 运行 `katago-remote status`，确认显示 `Running`。
3. 确认服务器可以访问外网 HTTPS。
4. 运行 `katago-remote restart`，使用新链接重试。

多个客户端可以同时连接，但共享同一块 GPU；如果感觉分析变慢，先关掉暂时不用的客户端。

## LizzieYzy Next 连上后立刻报 maxVisits

症状：`[remote-ws] 自建算力已连接` 之后，`kata-analyze` 立刻返回：

```text
[remote-ws] 自建算力错误：maxVisits must be between 1 and 1000000
```

KaTrain 等客户端却正常。

原因：LizzieYzy Next 的实时分析固定发送 `maxVisits: 10000000`，v0.1.1 及更早版本把超过服务端上限（1,000,000）的请求当成协议错误。

解决：升级到 v0.1.2 或更新。服务端改为把超限值截断到上限，客户端不需要改任何设置。已有 `config.json` 会保留旧安装器写入的 `maxVisits: 1000000`；想让上限也变成 10,000,000，再执行：

```bash
katago-remote init --max-visits 10000000
katago-remote restart
```

## 不小心公开了链接

立即运行：

```bash
katago-remote link list             # 找到泄露的那条链接
katago-remote link delete <id>      # 删除它（秒级断开）
# 或者只临时禁用：
katago-remote link set <id> --max-clients 0
```

`katago-remote reset-link` 仍然可用，它会轮换第一条链接的令牌并重启服务。

## 卸载

```bash
katago-remote stop
rm -f ~/.local/bin/katago-remote
rm -rf ~/.local/share/katago-remote-one-click
```

如果设置过 `KATAGO_REMOTE_HOME`，删除前请先核对实际目录。
