# Protocol and architecture

## Data path

```text
LizzieYzy Next or a compatible client
        |  WSS + private path token
        v
Cloudflare Quick Tunnel or named tunnel (optional fixed hostname)
        |  HTTP/WebSocket on localhost only
        v
katago-remote bridge
        |  line-delimited JSON over stdin/stdout
        v
KataGo Analysis Engine
```

The bridge transports KataGo's Analysis Engine JSON protocol, not raw GTP. A normal analysis request has no `action`; control requests use the upstream `terminate`, `terminate_all`, `query_version`, `query_models`, or `clear_cache` actions.

## Session isolation

- External query IDs are replaced with unique internal IDs before they reach KataGo.
- Results are mapped back to the originating ID before they reach the client.
- Disconnecting a client sends termination requests for its active analyses.
- Late results from a closed session are dropped.
- Any number of WebSocket clients may share the engine. Each connection gets its own session, ID space, and route table.
- The bridge keeps no per-client query queue or cap: a client may have as many analyses in flight as it sends. KataGo's `numAnalysisThreads` setting bounds how many of them search in parallel; the rest are queued by KataGo.
- `terminate_all` is scoped to the requesting session, so one client cannot cancel another client's analyses.

This prevents a reconnecting client from receiving results that belong to the previous connection.

## Safety boundaries

- The HTTP listener must resolve to `127.0.0.1` or `localhost`.
- The WebSocket endpoint requires a random token in its path. Cross-origin WebSocket upgrades are accepted because the path token is the only credential and no cookies are involved (browser clients such as a GitHub Pages build work without a whitelist). Tokens live in `state/links.json` (mode `600`); each link records a client limit (`-1` unlimited, `0` disabled, `N`), and the bridge reloads the registry every two seconds to apply edits.
- Token and runtime state files are created with user-only permissions.
- Messages are size-limited, and `maxVisits` above the configured ceiling (default 10,000,000) is clamped rather than rejected. A `maxVisits` of `0` is treated as "no explicit limit" and omitted before the request reaches KataGo. LizzieYzy Next's live analysis asks for 10,000,000 visits.
- The bridge launches KataGo directly without a shell and accepts no remote command-line input.
- The full private URL is printed only on explicit `start`, `show`, or `reset-link` commands and is omitted from service logs.

An optional Human SL model can be enabled with `-human-model`. The bridge forwards `overrideSettings` unchanged, so clients control `humanSLProfile`, `wideRootNoise`, and similar per-query settings.

Cloudflare terminates public TLS for both tunnel modes. A Quick Tunnel is suitable for personal and temporary sessions, not an SLA-backed multi-user service. A named tunnel serves a fixed hostname from your own Cloudflare domain; its token is stored under `state/tunnel-token` with mode `600` and passed to cloudflared through `TUNNEL_TOKEN` so it never appears in process arguments. In both modes the private path token remains the only access control. Stop the service when it is not needed.

## Compatibility

Compatibility requires a client that sends one KataGo Analysis Engine JSON object per WebSocket message or line. KaTrain-compatible WebSocket clients follow this transport convention. A generic GTP WebSocket client is not compatible without a separate GTP adapter.

Upstream reference: [KataGo Analysis Engine](https://github.com/lightvector/KataGo/blob/master/docs/Analysis_Engine.md).
