# Troubleshooting

Start with:

```bash
katago-remote doctor
katago-remote status
katago-remote logs
```

If the command is not in `PATH`, replace it with `~/.local/bin/katago-remote`.

## `nvidia-smi` is missing

The selected server image does not include a working NVIDIA driver. Recreate the instance with the provider's NVIDIA/CUDA image. Installing a driver into an arbitrary container is usually slower and less reliable than selecting the correct image.

## KataGo cannot load CUDA or cuDNN

Choose a CUDA 12 image with cuDNN 9. The installer tries the official cuDNN 9.8 KataGo build first and the official cuDNN 8.9.7 compatibility build second. It does not silently install or replace system GPU libraries.

Run this to preserve the exact diagnostic:

```bash
katago-remote doctor 2>&1 | tee katago-remote-doctor.txt
```

Do not include your complete WSS link in a public report.

## The link worked before a restart

The hostname is provided by a Cloudflare Quick Tunnel and is temporary. Restarting the service or server normally creates a different hostname:

```bash
katago-remote start
katago-remote show
```

Paste the newly displayed link into the client. A fixed hostname from a named tunnel does not change.

## The fixed hostname fails (1033 / 502 / 404)

First check that the tunnel's **Public Hostname** Service is `http://127.0.0.1:8765` and matches `listen` in `config.json` exactly.

Opening `https://katago.ykrsama.org/` in a browser:

- **404 / 401**: the tunnel works and the request simply lacked `/katago/<private-token>`. Expected.
- **1033**: the tunnel has no live connector. Run `katago-remote status` and `katago-remote logs`, and verify the token.
- **502**: Cloudflare cannot reach the origin. Check the Public Hostname port and whether `katago-remote show` works.
- **DNS does not resolve**: make sure the Public Hostname was saved and the CNAME for `katago.ykrsama.org` points at `<tunnel-id>.cfargotunnel.com`.

Never run the same tunnel token on several machines; Cloudflare load-balances across connectors and the link works only intermittently. See [Fixed hostname](FIXED-DOMAIN.md).

## The link changed on a fixed hostname

A named tunnel keeps its hostname; only the private token rotates. `restart` keeps the hostname, and `reset-link` only replaces the token. If the hostname itself changed, check `tunnelHostname` in `config.json` or run `katago-remote init --tunnel-hostname new.example.org`.

## Connection fails immediately

1. Copy the complete link, including `wss://`, `/katago/`, and the final token.
2. Check that `katago-remote status` says `Running`.
3. Confirm that the server can make outbound HTTPS connections.
4. Run `katago-remote restart` and try the new link.

Multiple clients can be connected at the same time, but they share the GPU. Close clients you are not using if analysis feels slower.

## LizzieYzy Next fails with maxVisits right after connecting

Symptom: after `[remote-ws] 自建算力已连接`, `kata-analyze` immediately returns:

```text
[remote-ws] 自建算力错误：maxVisits must be between 1 and 1000000
```

KaTrain and other clients work fine.

Cause: LizzieYzy Next always sends `maxVisits: 10000000` for live analysis, and v0.1.1 and older treated values above the server ceiling (1,000,000) as protocol errors.

Fix: upgrade to v0.1.2 or newer. The server now clamps oversized values instead of failing, so no client-side change is required. Existing `config.json` files keep the `maxVisits: 1000000` written by the old installer; to use the new 10,000,000 ceiling, run:

```bash
katago-remote init --max-visits 10000000
katago-remote restart
```

## LizzieYzy Next connects but shows no candidate moves

Symptom: `自建算力已连接` appears, `kata-analyze` produces only an `Unexpected or unused field ... playoutDoublingAdvantage` warning, no candidate moves are displayed, and the next analysis reports `another websocket analysis query is still active`.

Cause: v0.1.2 and older treated any KataGo warning as the end of the analysis and removed the query route, so every later result was dropped. LizzieYzy Next sends `playoutDoublingAdvantage` at the top level, KataGo warns about the unknown field, and that warning triggered the bug.

Fix: upgrade to v0.1.4 or newer. It keeps the query alive after warnings and moves the top-level `playoutDoublingAdvantage` into `overrideSettings`, which also removes the warning.

## Browser client handshake fails: Origin not authorized

Symptom: the service log shows

```text
WebSocket handshake failed: ... request Origin "xxx.github.io" is not authorized for Host "katago.ykrsama.org"
```

Cause: v0.1.2 and older only allowed same-origin WebSocket connections, so a browser page served from another domain (for example a GitHub Pages build) was rejected.

Fix: upgrade to v0.1.4 or newer. The server now accepts any Origin. Authentication still relies only on the private token in the link; no client-side change is needed.

## The link was accidentally shared

Revoke it immediately:

```bash
katago-remote link list             # find the leaked link
katago-remote link delete <id>      # delete it (disconnects within seconds)
# or just disable it temporarily:
katago-remote link set <id> --max-clients 0
```

`katago-remote reset-link` still works as a shortcut for rotating the first link and restarting.

## Uninstall

Stop the service first:

```bash
katago-remote stop
rm -f ~/.local/bin/katago-remote
rm -rf ~/.local/share/katago-remote-one-click
```

Review the paths before running the removal commands if `KATAGO_REMOTE_HOME` was customized.
