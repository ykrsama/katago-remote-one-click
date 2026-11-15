<div align="center">

# KataGo 远程算力一键部署

**让 Linux NVIDIA GPU 服务器一条命令变成可直接粘贴到围棋软件的私密 WSS 算力。**

[![CI](https://github.com/ykrsama/katago-remote-one-click/actions/workflows/ci.yml/badge.svg)](https://github.com/ykrsama/katago-remote-one-click/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/ykrsama/katago-remote-one-click)](https://github.com/ykrsama/katago-remote-one-click/releases/latest)
[![License](https://img.shields.io/github/license/ykrsama/katago-remote-one-click)](LICENSE)

[English](README.md) | [固定域名](docs/FIXED-DOMAIN.zh-CN.md) | [故障排查](docs/TROUBLESHOOTING.zh-CN.md) | [安全说明](SECURITY.md)

</div>

不需要配置防火墙、域名、证书、Python 环境或 Cloudflare 账号。安装器会自动准备 KataGo、官方 Transformer 权重、安全连接服务和加密临时通道，最后直接显示可用的 WSS 链接与二维码。想用自己的 Cloudflare 域名做固定地址，见[固定域名配置](docs/FIXED-DOMAIN.zh-CN.md)。

## 一条命令完成

准备一台 **Linux x86_64 + NVIDIA 显卡**的服务器。租用服务器时优先选择带 **CUDA 12 + cuDNN 9** 的系统镜像，然后在终端运行：

```bash
curl -fsSL https://raw.githubusercontent.com/ykrsama/katago-remote-one-click/v0.1.3/install.sh | bash
```

首次安装会下载数百 MB 文件，耗时取决于服务器网络。每个文件使用前都会核对固定 SHA-256，下载损坏或内容被替换时会立即停止。

安装成功后会看到类似下面的私密链接和二维码：

```text
wss://example-name.trycloudflare.com/katago/<私密令牌>
```

打开 LizzieYzy Next 的 **远程算力 > 自建算力**，粘贴完整链接，再点击 **一键启用自建算力**；也可以直接导入生成的二维码。

> 完整 WSS 链接相当于密码，不要发到群聊、论坛、Issue 或公开截图中。拿到链接的人可以在服务运行期间使用这台 KataGo。

## Google Colab

没有自己的 GPU 服务器时，可以使用带步骤说明的 Colab 笔记本。Colab 是否提供 GPU、GPU 型号和使用时长都由 Google 决定，本项目无法保证。

[![在 Colab 中打开](https://colab.research.google.com/assets/colab-badge.svg)](https://colab.research.google.com/github/ykrsama/katago-remote-one-click/blob/v0.1.3/colab/KataGo_Remote_One_Click.ipynb)

## 安装了什么

| 组件 | 固定版本 | 用途 |
| --- | --- | --- |
| KataGo | 1.18.2 CUDA 12.1 | 官方分析引擎 |
| Transformer 10B | 官方中型权重 | 棋力、速度和显存占用更均衡的默认模型 |
| cloudflared | 2026.8.3 | 建立加密 WSS 通道（临时或固定域名） |
| katago-remote | 0.1.3 | 安全协议桥接、链接生成和启停管理 |

文件默认放在 `~/.local/share/katago-remote-one-click`。服务只监听服务器自身的 `127.0.0.1`，不会要求开放公网入站端口；WSS 路径使用随机 256 位令牌；允许多个客户端同时连接，单个客户端也可以同时提交多个分析请求；它们共享同一块 GPU，同时分析会互相分摊速度。

## 日常使用

```bash
katago-remote show        # 重新显示所有私密链接和二维码
katago-remote status      # 查看是否正在运行
katago-remote logs        # 查看最近的错误信息
katago-remote restart     # 重启 KataGo；临时域名会变化，固定域名不变
katago-remote stop        # 停止引擎和连接
katago-remote reset-link  # 轮换第一条链接的令牌并重启
katago-remote link create --max-clients 2  # 新建私密链接（-1 无限制，0 禁用）
katago-remote link list    # 列出链接、并发上限和 URL
katago-remote doctor      # 检查显卡、文件和 KataGo 是否能启动
```

如果新开的终端提示找不到 `katago-remote`，使用完整命令：

```bash
~/.local/bin/katago-remote show
```

`trycloudflare.com` 域名是临时的，服务重启或服务器重启后通常会变化。重新运行 `katago-remote show`，把新链接粘贴到 LizzieYzy Next 即可。

## 私密链接管理

服务支持多条私密链接，每条可以单独限制同时连接的客户端数量：

```bash
katago-remote link create --max-clients 2   # 新建一条，最多 2 个客户端
katago-remote link create                   # 不传即为无限制
katago-remote link list                     # 列出 ID、上限和完整 URL
katago-remote link set 7ee5cc36 --max-clients 0    # 0 = 暂时禁用
katago-remote link set 7ee5cc36 --max-clients -1   # -1 = 无限制
katago-remote link delete 7ee5cc36          # 删除并断开它的在用会话
```

- 链接 ID 可以只写前几位前缀，也可以直接传完整 token。
- 修改在 2 秒内生效：降低上限会断开超出的连接（保留最早建立的），`--max-clients 0` 会拒绝新连接并断开现有连接，删除链接会断开它的所有会话。
- 从 v0.1.0 起使用的旧令牌会在首次运行时自动迁移成一条无限制链接，原 URL 不变。
- `reset-link` 仍保留，等价于轮换第一条链接的令牌并重启服务。

## 固定域名（可选）

如果域名已经托管在 Cloudflare，可以改用命名隧道，让链接固定成 `wss://katago.ykrsama.org/katago/<私密令牌>`，重启服务、重启服务器、更换服务器公网 IP 都不需要重新粘贴。

入口配置在 Cloudflare 控制台，`cloudflared` 运行在 GPU 服务器（生产环境）上；本机开发环境不需要运行它。完整步骤见[固定域名配置](docs/FIXED-DOMAIN.zh-CN.md)。

## 使用已有引擎或权重

懂参数的用户可以让安装器直接使用服务器上已有的文件。先单独下载安装器，确保下面的变量传给 Bash，而不是只传给 `curl`：

```bash
curl -fsSLo /tmp/katago-remote-install.sh \
  https://raw.githubusercontent.com/ykrsama/katago-remote-one-click/v0.1.3/install.sh

KATAGO_REMOTE_KATAGO_PATH=/opt/katago/katago \
KATAGO_REMOTE_MODEL_PATH=/opt/katago/model.bin.gz \
KATAGO_REMOTE_CONFIG_PATH=/opt/katago/analysis.cfg \
bash /tmp/katago-remote-install.sh
```

这些路径必须已经存在。安装器只记录路径，不复制、修改或删除自定义文件。

## Human SL 模型（可选）

KaTrain 的 `humanSLProfile` 分析需要 KataGo 的 Human SL 模型。默认安装不包含它，启用方式二选一：

```bash
# 安装时自动下载（约 95 MB）
KATAGO_REMOTE_HUMAN_MODEL=1 bash /tmp/katago-remote-install.sh

# 或者使用已有文件
KATAGO_REMOTE_HUMAN_MODEL_PATH=/opt/katago/b18c384nbt-humanv0.bin.gz \
  bash /tmp/katago-remote-install.sh
```

已经装好的服务器可以直接追加：

```bash
katago-remote init --human-model /opt/katago/b18c384nbt-humanv0.bin.gz
katago-remote restart
```

`doctor` 会多出 `Human model` 检查项。启用后，客户端在查询的 `overrideSettings` 里传 `humanSLProfile`（例如 `rank_4k`）即可，不传时行为与之前完全一致。国内网络下载 95 MB 可能较慢，可以先手动下载再指向文件。

## 当前支持范围

首版支持 Linux x86_64 NVIDIA GPU 服务器，以及使用 KataGo Analysis JSON over WebSocket 的客户端，包括 LizzieYzy Next 和 KaTrain 兼容客户端。首版不支持原始 GTP over WebSocket、多人共享、CPU 服务器或 Windows 服务器；需要自己的固定域名时，使用[命名隧道](docs/FIXED-DOMAIN.zh-CN.md)。

遇到问题时请先打开[中文故障排查](docs/TROUBLESHOOTING.zh-CN.md)。技术实现见[协议与架构说明](docs/PROTOCOL.md)。

## 致谢

本项目使用 [KataGo](https://github.com/lightvector/KataGo)、[KaTrain](https://github.com/sanderland/katrain) 和 [cloudflared](https://github.com/cloudflare/cloudflared) 的公开能力，是独立社区项目，与上述项目及 Cloudflare 无隶属关系。
