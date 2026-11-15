# Fixed hostname with your own Cloudflare domain

The default `trycloudflare.com` hostname comes from a Cloudflare Quick Tunnel and normally changes whenever the service or server restarts. If your domain (for example `ykrsama.org`) is hosted on Cloudflare, switch to a **named tunnel** and keep a stable address such as `wss://katago.ykrsama.org/katago/<private-token>`.

> Named tunnels are available from v0.1.1. The v0.1.0 release binary only supports quick tunnels; if the server still runs it, upgrade as shown in 2a.

## How the pieces connect

```text
LizzieYzy Next
    |  wss://katago.ykrsama.org/katago/<private-token>   TLS terminated by Cloudflare
    v
Cloudflare named tunnel (fixed hostname)
    |  encrypted outbound tunnel; no inbound port is opened
    v
cloudflared on the production server
    |  http://127.0.0.1:8765
    v
katago-remote bridge -> KataGo
```

Key points:

- `cloudflared` must run on the **GPU server, the production host**. The hostname lives in Cloudflare, but the compute stays on that server.
- Do not run the same token on your development machine. A tunnel with multiple connectors load-balances between them, so the link will work intermittently.
- A fixed hostname is not a public endpoint: the path still carries a random 256-bit token.

## Step 1: Cloudflare (one time)

1. Open the [Zero Trust dashboard](https://one.dash.cloudflare.com/) and go to **Networks → Tunnels → Create a tunnel**.
2. Choose **Cloudflared**, name it `katago-prod`, then copy the **token** (the long string starting with `eyJ`).
3. Add a **Public Hostname** to the tunnel:
   - **Subdomain**: `katago`
   - **Domain**: `ykrsama.org`
   - **Service**: `http://127.0.0.1:8765`
4. Save. Cloudflare creates the CNAME for `katago.ykrsama.org` automatically; do not add the DNS record by hand.

**The Service value must match the service's listen address** (default `127.0.0.1:8765`). Using `https://`, a public IP, or a different port makes the hostname return 1033 or 502.

## Step 2: Production server

### 2a. Existing installation (upgrade from v0.1.0)

Check the binary first:

```bash
~/.local/bin/katago-remote version   # v0.1.3 or newer is required
```

The easiest upgrade is to rerun the installer with the tunnel variables. It only re-downloads the binary, because its hash changed, and reuses KataGo, the model, and the config:

```bash
curl -fsSLo /tmp/katago-remote-install.sh \
  https://raw.githubusercontent.com/ykrsama/katago-remote-one-click/v0.1.3/install.sh

KATAGO_REMOTE_TUNNEL_HOSTNAME=katago.ykrsama.org \
KATAGO_REMOTE_TUNNEL_TOKEN='eyJ...' \
bash /tmp/katago-remote-install.sh
```

To avoid rerunning the installer, replace only the binary as shown in 2c and then run:

```bash
umask 077
printf '%s\n' 'eyJ...your-tunnel-token...' > /root/katago-tunnel-token

~/.local/bin/katago-remote init \
  --tunnel-mode named \
  --tunnel-hostname katago.ykrsama.org \
  --tunnel-token-file /root/katago-tunnel-token

~/.local/bin/katago-remote restart
```

`init` only overwrites the fields you pass, so existing KataGo, model, config, listen address, and `maxVisits` values are kept.

The token is copied to `~/.local/share/katago-remote-one-click/state/tunnel-token` with mode `600` and handed to cloudflared through the `TUNNEL_TOKEN` environment variable, so it never appears in the process arguments. `/root/katago-tunnel-token` is only the input for `init` and can be deleted afterwards.

### 2b. New installation

With v0.1.3 or newer, pass the variables to **Bash**, not to `curl`:

```bash
curl -fsSLo /tmp/katago-remote-install.sh \
  https://raw.githubusercontent.com/ykrsama/katago-remote-one-click/v0.1.3/install.sh

KATAGO_REMOTE_TUNNEL_HOSTNAME=katago.ykrsama.org \
KATAGO_REMOTE_TUNNEL_TOKEN='eyJ...' \
bash /tmp/katago-remote-install.sh
```

Use `KATAGO_REMOTE_TUNNEL_TOKEN_FILE=/root/katago-tunnel-token` to keep the token out of your shell history. In a `curl ... | bash` pipeline the variables only reach `curl`, which is why the script is downloaded first.

### 2c. Build from source (optional)

Use this only to replace the binary manually or to verify the source. Any machine with Go can produce the Linux binary, including your development machine:

```bash
git clone https://github.com/ykrsama/katago-remote-one-click
cd katago-remote-one-click
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
  -trimpath -buildvcs=false \
  -ldflags "-s -w -X main.version=v0.1.3" \
  -o katago-remote-linux-amd64 ./cmd/katago-remote
```

Copy it to the server, replace the service binary, and restart:

```bash
install -m 700 katago-remote-linux-amd64 \
  ~/.local/share/katago-remote-one-click/bin/katago-remote
~/.local/bin/katago-remote restart
```

`~/.local/bin/katago-remote` is a symlink to that file, so only the target needs replacing.

## Step 3: Verify

```bash
katago-remote doctor
katago-remote status
katago-remote show
```

In named-tunnel mode `doctor` also reports `Tunnel hostname`, `Tunnel token`, and `Tunnel ingress`.

Open `https://katago.ykrsama.org/` in a browser:

- 404 or 401: the tunnel works and the private token was simply missing. Expected.
- 1033: the tunnel has no live connector. Check that cloudflared is running on the server.
- 502: Cloudflare cannot reach the origin. Check that the Public Hostname Service equals `http://127.0.0.1:8765`.

A browser request does not prove WebSocket works; paste the full link into LizzieYzy Next to confirm.

## Maintenance

| Goal | Action |
| --- | --- |
| New private link, same hostname | `katago-remote reset-link` |
| Rotate the Cloudflare token | Regenerate the token in the dashboard, update `/root/katago-tunnel-token`, repeat 2a |
| Change the hostname | Update the Public Hostname, then `init --tunnel-hostname new.example.org` and `restart` |
| Stop the service | `katago-remote stop` (DNS still resolves, but returns 1033) |
| Return to a quick tunnel | `katago-remote init --tunnel-mode quick` and `restart` |

## Common mistakes

- **Running cloudflared on the development machine**: the hostname points at the laptop instead of the GPU server.
- **Running the same token twice**: Cloudflare load-balances across connectors, so the link works intermittently.
- **Wrong Public Hostname Service**: `https://`, a public IP, or a different port all produce 502.
- **Assuming a fixed hostname removes the token**: the private token is still the only access control. Run `reset-link` if it leaks.
