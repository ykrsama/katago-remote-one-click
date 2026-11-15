<div align="center">

# KataGo Remote One-Click

**Turn a Linux NVIDIA GPU server into a private KataGo WSS endpoint with one command.**

[![CI](https://github.com/ykrsama/katago-remote-one-click/actions/workflows/ci.yml/badge.svg)](https://github.com/ykrsama/katago-remote-one-click/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/ykrsama/katago-remote-one-click)](https://github.com/ykrsama/katago-remote-one-click/releases/latest)
[![License](https://img.shields.io/github/license/ykrsama/katago-remote-one-click)](LICENSE)

[中文说明](README.zh-CN.md) | [Fixed hostname](docs/FIXED-DOMAIN.md) | [Troubleshooting](docs/TROUBLESHOOTING.md) | [Security](SECURITY.md)

</div>

No firewall rule, domain, TLS certificate, Python environment, or Cloudflare account is required. The installer prepares KataGo, a strong official Transformer model, an authenticated local bridge, and an encrypted temporary tunnel. It then prints the private WSS link and a QR code that LizzieYzy Next can use directly. To use your own Cloudflare domain as a permanent address, see [Fixed hostname](docs/FIXED-DOMAIN.md).

## One command

Use a Linux x86_64 server with an NVIDIA GPU. Choose a CUDA 12 image with cuDNN 9 when your provider offers one, then run:

```bash
curl -fsSL https://raw.githubusercontent.com/ykrsama/katago-remote-one-click/v0.1.4/install.sh | bash
```

The command downloads several hundred megabytes. Every downloaded file is checked against a pinned SHA-256 digest before it is used.

When setup finishes, the terminal displays a private link similar to:

```text
wss://example-name.trycloudflare.com/katago/<private-token>
```

In LizzieYzy Next, open **Remote Compute > Custom Compute**, paste the complete link, and choose **Enable Custom Compute**. You can import the generated QR code instead.

> Treat the full WSS link like a password. Anyone who has it can use the running KataGo process until the link is revoked or the service stops.

## Google Colab

You can also use the guided notebook. Colab GPU availability and session duration are controlled by Google and are not guaranteed.

[![Open In Colab](https://colab.research.google.com/assets/colab-badge.svg)](https://colab.research.google.com/github/ykrsama/katago-remote-one-click/blob/v0.1.4/colab/KataGo_Remote_One_Click.ipynb)

## What gets installed

| Component | Pinned version | Purpose |
| --- | --- | --- |
| KataGo | 1.18.2 CUDA 12.1 | Official analysis engine |
| Transformer 10B | official medium model | Strong default model with practical GPU usage |
| cloudflared | 2026.8.3 | Encrypted WSS tunnel (temporary or fixed hostname) |
| katago-remote | 0.1.4 | Authenticated analysis-protocol bridge and service manager |

Files are installed under `~/.local/share/katago-remote-one-click`. The bridge listens only on `127.0.0.1`; no inbound server port is opened. A random 256-bit token protects the WebSocket path. Multiple clients may connect at the same time, and each client may keep several analysis queries in flight; they all share the GPU, so concurrent analyses divide the available speed.

## Everyday commands

```bash
katago-remote show        # Show all private links and the QR code
katago-remote status      # Check whether the service is running
katago-remote logs        # Show recent diagnostics
katago-remote restart     # Restart KataGo; a quick hostname changes, a fixed one does not
katago-remote stop        # Stop KataGo and the tunnel
katago-remote reset-link  # Rotate the first link token and restart
katago-remote link create --max-clients 2  # Create a link (-1 unlimited, 0 disabled)
katago-remote link list    # List links, limits, and URLs
katago-remote doctor      # Check GPU, files, and KataGo startup
```

If `katago-remote` is not found in a new terminal, run it as:

```bash
~/.local/bin/katago-remote show
```

The `trycloudflare.com` hostname is temporary and usually changes after a restart or server reboot. Run `katago-remote show`, then update the link in LizzieYzy Next.

## Private link management

The service supports multiple private links, each with its own concurrent-client limit:

```bash
katago-remote link create --max-clients 2   # at most two clients
katago-remote link create                   # no flag means unlimited
katago-remote link list                     # ID, limit, and full URL
katago-remote link set 7ee5cc36 --max-clients 0    # 0 = temporarily disabled
katago-remote link set 7ee5cc36 --max-clients -1   # -1 = unlimited
katago-remote link delete 7ee5cc36          # delete and disconnect its sessions
```

- A link ID may be an unambiguous prefix, or you can pass the full token.
- Edits apply within two seconds: lowering a limit disconnects the newest extra sessions, `--max-clients 0` refuses new connections and disconnects existing ones, and deleting a link disconnects all of its sessions.
- The legacy token from v0.1.0 is migrated into an unlimited link on first use, so the existing URL keeps working.
- `reset-link` remains as a shortcut for rotating the first link and restarting.

## Fixed hostname (optional)

If your domain is hosted on Cloudflare, switch to a named tunnel and keep `wss://katago.ykrsama.org/katago/<private-token>` stable across restarts, reboots, and server IP changes.

The ingress lives in the Cloudflare dashboard and `cloudflared` runs on the GPU server (production). Your development machine does not run it. See [Fixed hostname with your own Cloudflare domain](docs/FIXED-DOMAIN.md) for the full procedure.

## Use your own KataGo files

The installer can keep an existing KataGo executable, model, or analysis config in place. Download the reviewed installer first so the variables are passed to Bash rather than only to `curl`:

```bash
curl -fsSLo /tmp/katago-remote-install.sh \
  https://raw.githubusercontent.com/ykrsama/katago-remote-one-click/v0.1.4/install.sh

KATAGO_REMOTE_KATAGO_PATH=/opt/katago/katago \
KATAGO_REMOTE_MODEL_PATH=/opt/katago/model.bin.gz \
KATAGO_REMOTE_CONFIG_PATH=/opt/katago/analysis.cfg \
bash /tmp/katago-remote-install.sh
```

The paths must already exist on the server. The installer does not copy or modify these custom files.

## Human SL model (optional)

KaTrain's `humanSLProfile` analysis needs KataGo's Human SL model. It is not installed by default. Enable it in one of two ways:

```bash
# Download it during installation (about 95 MB)
KATAGO_REMOTE_HUMAN_MODEL=1 bash /tmp/katago-remote-install.sh

# Or point at an existing file
KATAGO_REMOTE_HUMAN_MODEL_PATH=/opt/katago/b18c384nbt-humanv0.bin.gz \
  bash /tmp/katago-remote-install.sh
```

An existing installation can enable it in place:

```bash
katago-remote init --human-model /opt/katago/b18c384nbt-humanv0.bin.gz
katago-remote restart
```

`doctor` then reports a `Human model` check. Once enabled, clients pass `humanSLProfile` (for example `rank_4k`) in the query's `overrideSettings`; queries without it behave exactly as before. The 95 MB download can be slow on some networks, so downloading it separately and pointing at the file also works.

## Scope

Version 0.1 supports Linux x86_64 NVIDIA GPU servers and clients that implement KataGo's line-delimited Analysis Engine protocol over WebSocket, including LizzieYzy Next and KaTrain-compatible clients. It does not support raw GTP-over-WebSocket, multi-user hosting, CPU-only servers, or Windows servers. A permanent address on your own domain is available through a [named tunnel](docs/FIXED-DOMAIN.md).

See [the protocol and architecture notes](docs/PROTOCOL.md) for technical details and [troubleshooting](docs/TROUBLESHOOTING.md) for copy-and-run fixes.

Maintainers can run `scripts/test-public-tunnel.sh` to verify a real authenticated WSS round trip through a pinned cloudflared binary. The normal test suite skips this network integration test.

## Acknowledgements

This project integrates the public interfaces of [KataGo](https://github.com/lightvector/KataGo), [KaTrain](https://github.com/sanderland/katrain), and [cloudflared](https://github.com/cloudflare/cloudflared). It is an independent community project and is not affiliated with those projects or Cloudflare.
