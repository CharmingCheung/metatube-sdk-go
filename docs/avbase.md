# AVBASE in this fork

AVBASE is included in normal server/image builds. Public metadata is read from
`__NEXT_DATA__.props.pageProps` in the search/detail HTML: one page per operation,
without a homepage/build-ID lookup or `/_next/data/<build>/...` dependency.
Existing `AVBASE` provider names and `prefix:work_id` identifiers remain valid.
No database migration or re-scrape is required. Existing cached metadata remains
available through the server's normal cache; force refresh only selected items
when needed.

## Start without an extra proxy

Use the fork image `ghcr.io/charmingcheung/metatube-server:dev` once published,
or build locally with `docker build -t metatube-avbase .`. Keep your existing
DSN, database volume, token, ports and other provider configuration.

```yaml
environment:
  MT_MOVIE_PROVIDER_AVBASE__PRIORITY: "1010"
  MT_MOVIE_PROVIDER_AVBASE__REQUEST_INTERVAL: 5s
  MT_MOVIE_PROVIDER_AVBASE__BLOCKED_COOLDOWN: 10m
  MT_MOVIE_PROVIDER_AVBASE__TIMEOUT: 90s
```

Configuration follows MetaTube v1.4's **double underscore** syntax. The old
`MT_MOVIE_PROVIDER_PRIORITY_AVBASE` variable from older issue comments is not
read by this version. Confirm `AVBASE` appears in `/v1/providers` after restart.

| Suffix after `MT_MOVIE_PROVIDER_AVBASE__` | Default | Meaning |
| --- | --- | --- |
| `PRIORITY` | 996 | Existing provider priority; use 1010 to prefer AVBASE, 0 to disable |
| `REQUEST_INTERVAL` | `3s` | Minimum spacing, 1s–1m; all metadata operations on this instance serialize |
| `BLOCKED_COOLDOWN` | `5m` | Pause after 403/challenge, 30s–24h |
| `MAX_RETRIES` | `2` | Additional attempts for 429/500/502/503/504, 0–3 |
| `TIMEOUT` | global timeout (`1m`) | Overall page-operation budget, including queueing, pacing, body and retries |
| `PROXY` | standard Go environment proxy | HTTP/HTTPS/SOCKS proxy for AVBASE page requests only |
| `SOURCE_ENRICHMENT` | `false` | Opt into the legacy additional store lookups for supplementary metadata |

429 and transient server errors honor `Retry-After` and use at least 5s/10s/20s
backoff. A wait beyond the operation budget returns an explicit error and retains
the cooldown for the next caller. 403 is never immediately retried. Transport,
certificate and parse errors surface directly rather than becoming an empty
search or a two-hour cached failure. There is no IP rotation, CAPTCHA solver or
promise that a site's access challenge will always accept the client.

Limits apply per AVBASE provider instance, not across multiple server replicas.
Image downloads and optional source enrichment retain the existing fetchers and
their own timeout/proxy behavior. Leave source enrichment off for fewer requests;
AVBASE's own title, cast, images, genres, director and runtime are used directly.
The adapter does not expose a new actor/talent provider or merge alias identities.

## Optional mitmproxy transport

An ordinary HTTPS CONNECT proxy tunnels Go's TLS connection unchanged. An
intercepting proxy such as mitmproxy establishes its own upstream TLS connection,
which can change how a site treats the request. This may explain the workaround
reported in issues, but IP reputation and challenge policy also matter; a proxy
is not a guaranteed fix and slowing down alone cannot solve every 403.

The example keeps certificate validation enabled on both connections, publishes
no proxy port, and sends only AVBASE metadata requests through the proxy. Unlike
the pasted workaround, it does not set `ssl_insecure=true` or globally proxy all
providers. Only the public CA certificate is mounted into MetaTube.

For a **new test instance** using `docker-compose.avbase.yaml`:

```sh
# Start proxy first so it can generate its CA files.
docker compose -f docker-compose.avbase.yaml up -d mitmproxy
# Wait until this public certificate exists; do not create an empty placeholder.
test -s mitmproxy/mitmproxy-ca-cert.pem
# Then start MetaTube. A missing certificate deliberately fails the bind mount.
docker compose -f docker-compose.avbase.yaml up -d metatube
```

Keep the `mitmproxy` directory private: it includes the proxy's private CA key.
`SSL_CERT_FILE` adds the public CA to Go's trust loading alongside the system
certificate directories. Do not disable TLS validation. To adapt an existing
PostgreSQL deployment, copy the proxy/environment/public-cert settings and keep
its existing DSN/volumes; this standalone SQLite example is not a migration plan.
The example server binds localhost; change the bind only for your intended LAN
or reverse-proxy deployment and keep your usual authentication configuration.

For a remote HTTP/SOCKS proxy without interception, configure just `PROXY`; no
custom CA is necessary. Standard `HTTP_PROXY`/`HTTPS_PROXY` environment behavior
also remains available when no provider-specific proxy is set.

## Build and publish

`make server` and the Dockerfile both include AVBASE without enabling unrelated
experimental providers. The Docker workflow publishes to the lowercase fork
owner's GHCR namespace, with `packages: write` permission. Pushes to `main` produce
`dev` and `sha-<12-character-commit>` images for amd64/arm64. The two architectures
build in parallel on `ubuntu-24.04` and `ubuntu-24.04-arm` native runners, with
separate caches. Each uploads its immutable image digest; only after both builds
succeed does a merge job publish the shared multiarch tags and verify both
architectures in the manifest. No QEMU is used in CI. Version tags produce
`latest` and the version tag. `workflow_dispatch` can rebuild main manually.
Enable GitHub Actions in the fork if GitHub has disabled inherited workflows.

Local checks:

```sh
GITHUB_ACTIONS=true go test -race ./provider/avbase -count=1
go vet ./provider/avbase ./engine
go build ./cmd/server
```

`GITHUB_ACTIONS=true` uses the upstream convention to skip external-site tests.
Live AVBASE tests are separate and deliberately small:

```sh
go test ./provider/avbase -run '^TestAVBase_SearchMovie$/^ABP-588$' -count=1 -v
go test ./provider/avbase -run '^TestAVBase_GetMovieInfoByID$/^prestige:ABP-588$' -count=1 -v
```

See [avbase-report.md](avbase-report.md) for actual verification and limitations.
