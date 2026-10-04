[English](README.md) | [简体中文](README.zh-CN.md)

# andey-proxy

A lightweight, all-in-one network toolkit for routers and low-power devices. It runs as a single binary with no runtime dependencies and ships with a built-in web console.

## Features

- **Web service / reverse proxy** — multiple site listeners, dispatch by hostname + path prefix
  - Dedicated HTTP/2 backend connection pools, round-robin across multiple backends, WebSocket, SSE and long-lived connections
  - Automatic trusted-proxy headers, Host passthrough, write-only custom request headers, Basic Auth, self-signed TLS to backends
  - Hot reload of child rules, connect/response timeouts, path-prefix stripping, backend connection testing
  - Optional rate limiting per direct client IP, request body size cap, Location and Cookie Domain/Path rewriting
  - Per-site "force HTTPS" switch: plaintext/TLS sniffing on the same port, plaintext requests get a 301
  - Per-site traffic panel: request count, status-code distribution, inbound/outbound bytes (in-memory, cleared on restart)
  - 301/302 redirects
  - Static file serving
- **HTTPS certificates** — ACME issuance and renewal (DNS-01, wildcards supported), certificates selected by SNI, falling back to a self-signed certificate when nothing matches
  - Providers: Alibaba Cloud, Cloudflare, DNSPod
- **Port forwarding** — TCP/UDP layer-4 forwarding with live logs
  - Per-rule `idleTimeout` for TCP (default 600s, API/config field only)
- **DDNS** — periodically detects IP changes and updates DNS records
  - IP source: network interface or custom API
  - IPv4/IPv6; Alibaba Cloud, Cloudflare, DNSPod
- **Cloudflare Tunnel** — create remotely managed tunnels or import an existing Tunnel token, manage multiple connectors, hostname routes, process status and logs
  - Publish project web sites through a private Unix socket, retaining authentication, visitor-IP rate limits and traffic statistics; or connect directly to HTTP/HTTPS services
  - Optional external `cloudflared` 2025.4.0+, installed manually or from the official release using the architecture-aware download button; no bundled binary or mandatory installation dependency. See [setup and behavior](docs/cloudflare-tunnel.md)
- **Access control** — IP allow/deny lists (CIDR supported) and User-Agent allow/deny lists, shared by port forwarding and the web service
- **Admin console** — Vue 3 + Element Plus, embedded into the binary with `go:embed`, nothing extra to deploy
- **Operations console** — health overview, firewall, manual updates and recent errors in one place
- **Log center** — structured query, download and audit, with a disk usage cap of about 5 MiB
- **Notification center** — manage channels and event subscriptions independently; currently Telegram Bot, filterable by certificate / DDNS / site / forwarding / tunnel event type
- **Config backup** — export and import an encrypted backup from the dashboard (passphrase-derived key, movable across devices)
- **Account security** — optional Google Authenticator two-factor authentication, one-time recovery codes and on-device reset

## Quick start

### Download

Grab the build for your architecture from [Releases](https://github.com/wubin0532/Reverse_Proxy/releases):

| Architecture | Target devices |
|------|---------|
| x86_64 | Standard Linux servers / x86 soft routers |
| arm64 | ARM64 routers, Raspberry Pi 4+ |
| armv7 | 32-bit ARM devices |
| mips / mipsle | MIPS routers such as MT7621 (softfloat) |

### Run

```bash
./andey-proxy                      # default admin port 16606, config dir ./andey-proxy-conf
./andey-proxy -p 8080              # use a different admin port
./andey-proxy -listen 192.168.1.1  # bind to one interface only (defaults to all interfaces)
./andey-proxy -cd /etc/andey-proxy # use a specific config directory
```

Then open `https://<device-ip>:16606`. When no ACME certificate matches, a self-signed one is used and the browser will ask you to confirm it on first visit. The default account is `admin`; the one-time random password is printed to the console on first start only.

If you must support legacy clients that cannot do HTTPS, pass `-admin-http` explicitly. That mode keeps a high-risk warning on the dashboard at all times.

### Build from source

Requires the Go version pinned in `go.mod` plus Node.js:

```bash
make build              # build frontend + local binary (outputs andey-proxy)
./scripts/build-all.sh  # cross-compile every architecture (outputs dist/)
```

Frontend assets are embedded into the binary via `go:embed`, so `make web` (or `cd web && npm ci && npm run build`) has to run before building the Go code.

### OpenWrt

`package/openwrt/` contains the OpenWrt package definition (including the LuCI files) and can be built into an ipk with the OpenWrt SDK; see the comments at the top of the Makefile in that directory. The default config directory is `/etc/andey-proxy`.

The `luci-app-andeyproxy_*.ipk` in each release contains both the main program and the LuCI interface (under the **Services → andey-Proxy** menu). Earlier versions split this into `andey-proxy` + `luci-app-andeyproxy`; when migrating from the old package name, back up before removing, because uninstalling the old package deletes runtime data:

```bash
cp -a /etc/andey-proxy /etc/andey-proxy.bak
cp -a /etc/config/andey-proxy /etc/andey-proxy.bak.uci
opkg remove andey-proxy
opkg install luci-app-andeyproxy_*.ipk   # postinst restores the config from the backup
```

On OpenWrt builds without opkg (ImmortalWrt 25.12 and later moved to apk), use the `.run` package instead — it carries the LuCI files too:

```bash
sh andey-proxy_*_linux_x86_64.run
```

When LuCI is detected, the installer lays out the menu entries, ACLs, settings page and translations, creates `/etc/config/andey-proxy` (`enabled=1`, port 16606) and starts the service, so there is no manual package install or config editing left to do. On plain Linux servers those LuCI files are ignored.

## Repository layout

```
├── main.go              # entry point: load config, start modules and the admin HTTP server
├── internal/
│   ├── webproxy/        # web service / reverse proxy core (site listeners, child rule dispatch, access logs)
│   ├── forward/         # TCP/UDP port forwarding
│   ├── ddns/            # DDNS scheduler and DNS provider implementations
│   ├── tunnel/          # external cloudflared supervisor, Cloudflare API and route/DNS synchronization
│   ├── acme/            # ACME issuance, renewal and SNI certificate serving (built on lego)
│   ├── guard/           # IP / User-Agent allow and deny lists
│   ├── adminweb/        # embedded frontend static assets
│   ├── api/             # admin REST API
│   ├── auth/            # login authentication
│   ├── logcenter/       # structured logs, rotation, download and audit
│   ├── notify/          # event bus, notification channel management, Telegram Bot delivery
│   └── config/          # AES-256-GCM encrypted config with atomic transactions
├── web/                 # frontend source (Vue 3 + Vite + Element Plus)
├── package/openwrt/     # OpenWrt ipk packaging
└── scripts/build-all.sh # multi-architecture cross compilation
```

## Configuration

All configuration is stored as a single AES-256-GCM encrypted blob. The device key is stored separately; on OpenWrt it defaults to `/etc/andey-proxy.key` with mode `0600`. DNS tokens, Basic Auth passwords and custom request headers are write-only fields — the API never returns them in plaintext.

Security notes: when backing up or migrating the config directory, the `.key` file (the config encryption key) is as sensitive as the config itself. Protect it, and never let it leak with a backup. Lose the `.key` and the existing encrypted config can no longer be decrypted. Dashboard backup export uses its own passphrase-derived key and is unaffected by `.key`. When you first reach the admin console through a self-signed certificate, compare the browser's certificate fingerprint against the SHA-256 fingerprint printed in the startup log before trusting it.

### Proxy behavior and resource limits

- Automatic backend failover retries only apply to bodyless GET, HEAD and OPTIONS, and switch backends at most once. Failed POST, PUT, PATCH and DELETE requests return an error directly so operations are never executed twice. Retries preserve the original path, query string and public Host information.
- Ambiguous paths containing `.` / `..` segments, repeated slashes or backslashes (including after decoding) are rejected with 400 before child rules and authentication are matched. Percent-encoding is preserved when stripping a path prefix.
- Static file serving is confined to the configured root directory and refuses symlinks that point outside it. Symlinks inside the root, hidden files and directory listings remain accessible, so only put content in the root that you intend to publish.
- Same-port HTTP/TLS sniffing keeps at most 128 pending connections per site with a 10-second wait for the first byte; beyond that, new connections are closed.
- Layer-4 port forwarding shares a global cap of 256 TCP connections and 256 active UDP sessions across all rules. At the cap, new TCP connections are closed and new UDP session packets are dropped, while existing sessions keep running. Each UDP rule's session index is also capped at 256 entries. These are fixed limits in the current version.
- Hot rule reload only rebuilds the handlers and connection pools that changed. Requests holding an old rule snapshot that have not yet acquired a handler may receive a 503; subsequent requests use the new config.
- Config backup v1 accepts only the fixed scrypt parameters the program uses when exporting (about 32 MiB of derivation memory). Import and export are mutually exclusive and return 409 while another operation is running. Malformed encrypted fields are rejected before the key is derived.

### Google Authenticator two-factor authentication

Open **Account security** in the top-right of the admin panel and enter your current password to bind Google Authenticator or any other RFC 6238-compatible authenticator. Two-factor authentication is off by default; enabling it, disabling it or regenerating recovery codes invalidates all existing sessions and requires logging in again.

The 10 recovery codes generated during binding are shown once — download them or store them offline. Because the admin console can be served over plaintext HTTP, the second login step and all two-factor management endpoints are rejected in that mode, so the code and the binding secret cannot be eavesdropped on.

If both the authenticator and the recovery codes are lost, stop the service on the device itself and run a one-time reset:

```bash
/etc/init.d/andey-proxy stop
/usr/bin/andey-proxy -cd /etc/andey-proxy -reset-totp
/etc/init.d/andey-proxy start
```

The reset command confirms through the config lock that the service has stopped and refuses to modify the config while it is still running. It only disables two-factor authentication; the admin password and other settings are untouched.

## Manual update

Main-program updates remain manual; uploaded package scripts are never executed. Optional cloudflared checks and binary downloads are available separately on the Tunnel page after administrator confirmation. Download the signed `.run` package for your architecture yourself, then use **Upload package to update manually** under **Settings → Updates & backups**: verify the signature, digest, Linux/CPU/ELF architecture and version before entering the admin password to install.

Release packages are signed with Ed25519. Building a `.run` requires the release private key to be supplied through `RELEASE_SIGNING_KEY`; GitHub Actions reads it from a protected secret named `RELEASE_SIGNING_PRIVATE_KEY`. The private key must never be committed to the repository or written into build artifacts.

## CI

On every push to `main` or `v*` tag, GitHub Actions reads the Go version from `go.mod` and runs frontend tests and build, `go vet ./...`, `go test -race ./...`, a dependency vulnerability scan, and builds all five architecture variants. Tag builds additionally produce signed `.run` packages and OpenWrt `.ipk` artifacts for all five architectures (see [Actions](https://github.com/wubin0532/Reverse_Proxy/actions)).

## Security deployment notes

- Use a dedicated hostname and port for the admin console instead of sharing a domain with public sites, to reduce scanning and cross-site attack surface.
- In production, use `-listen` to bind the admin console to an internal address rather than exposing it to the internet. Binding to all interfaces by default prints a warning in the startup log.
- The read-only ACL for the OpenWrt LuCI interface grants only status queries plus read access to `/etc/andey-proxy/initial-password`. That file exists only for the first login and is deleted automatically once the administrator changes the password. To stop read-only accounts from seeing it, remove that grant line in `luci-app-andeyproxy.json`.
- The legacy packages in the repository's `release/` directory fail their own signature self-check. They are leftovers from before the security audit — do not redistribute or install them, and wait for a freshly signed release instead.

## License

[Apache-2.0](LICENSE)
