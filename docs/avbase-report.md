# AVBASE adaptation report — 2026-10-08

## Scope and starting state

Repository: `CharmingCheung/metatube-sdk-go`, clean `main` at `19a92ad` before this
session. Changes are confined to this fork; CharmingReel is unchanged. The
CharmingReel product/architecture/status and integration report were reviewed.
Rust/Web acceptance commands apply to that separate application and were not
run for these Go-only changes.

## Findings

- AVBASE exists in source but was registered only with `experimental`; the
  standard Dockerfile's `make server` omitted it.
- The old adapter required a homepage build ID for Next.js data routes.
  `singledo` cached both success and failure for two hours. A short initial
  failure could therefore prevent recovery long after the site recovered.
- A command-line homepage request returned Cloudflare HTTP 403 with
  `cf-mitigated: challenge`. Chrome loaded the homepage and search/detail pages.
  The original Go adapter also successfully searched one title at this location.
  These observations do **not** establish a universal Go TLS fingerprint ban or
  prove an IP-based cause; network/client paths differ.
- Current search/detail HTML still embeds `__NEXT_DATA__.props.pageProps` with
  `works`/`work`, source products, casts and JS-style date strings. Browser and
  Go requests confirmed compatibility with the fields used here.
- The old search filter dropped records without a supported source adapter,
  even when AVBASE supplied usable data, and omitted prefixes in result URLs.

## Changes

- Read public search/detail HTML directly, removing build-ID dependence and the
  extra homepage request. Retain exported `GetBuildID` for SDK callers without
  caching errors. No generic singledo or other providers were changed.
- Serialize metadata requests per instance with a default 3s interval. A total
  timeout bounds queueing, pacing, body reads and retries. Responses are capped
  at 4 MiB. Cookies and a stable User-Agent are reused with verified TLS.
- Do not immediately retry 403/challenge responses; pause the source for 5m.
  Honor Retry-After and bounded exponential backoff for 429/500/502/503/504,
  sharing the cooldown across search/detail callers. Parse failures and malformed
  pages are errors rather than successful empty results.
- Add provider-specific configuration through the existing config interface,
  including a proxy override. Leave external source enrichment opt-in and use
  AVBASE's own actor names, genres, director/runtime and image references by
  default. Preserve provider namespace and canonical prefix/work identifiers.
- Register AVBASE in normal builds. Target the fork owner's GHCR namespace and
  grant the Docker workflow package-write permission; add commit-specific tags.
- Document direct and optional mitmproxy deployment. Keep upstream TLS checks,
  mount only the public CA certificate in MetaTube, and publish no proxy port.
  Exclude local proxy key/database material from Git and Docker build contexts.

## Verification

- Focused race tests: `GITHUB_ACTIONS=true go test -race ./provider/avbase ./engine
  -count=1` passed. Cover missing/invalid JSON, real empty results, recovery,
  shared 403 cooldown, Retry-After seconds/date, transient retry, retry disabling,
  serialization/pacing, queue timeout, response size, TLS rejection, proxy routing,
  optional enrichment/fallback, canonical IDs, dates, runtime and normal-build
  registration/priority disablement. Two existing AVBASE live test functions are
  skipped under the upstream CI flag.
- Upstream CI scope: `GITHUB_ACTIONS=true DOCKER_HOST=<local Docker socket> go test
  $(go list ./... | rg -v '/translate') -json` passed: 62 packages passed, 19 had
  no tests; 235 test/subtest pass events and 73 skips. Includes local PostgreSQL
  and SQLite database/cache compatibility tests. Skips include inherited
  external-site tests and the PostgreSQL-only fuzzy case in SQLite.
- The broader `go test ./...` attempt had five failures in existing online
  translation packages: Baidu, Google, GoogleFree, DeepL and OpenAI. These depend
  on external availability/credentials (OpenAI returned missing-key 401; DeepL
  returned 403). Upstream CI already excludes `/translate`; no such tests were
  changed or presented as passing.
- Full configured `golangci-lint run ./...` passed after replacing two literal
  HTTP status codes in new tests with standard constants.
- `go vet ./...`, changed-file `gofmt` verification, `git diff --check`, and Docker
  Compose configuration validation passed.
- Actual Go live requests passed: search `ABP-588`; details `prestige:ABP-588`
  and `SSIS-354`. The prefixed ID, number, actor names and metadata were returned.
- Local ARM64 Docker image built successfully. An isolated container exposed
  AVBASE in `/v1/providers`; source-restricted search with `fallback=false` and
  detail for `prestige:ABP-588` both returned HTTP 200 with correct identifiers
  and actors. No existing service or database was replaced.

- Optional proxy live check also passed: an isolated mitmproxy 12.2.3 container
  and a second MetaTube container searched and fetched `SSIS-354`, both HTTP 200.
  Proxy logs confirmed exactly the two AVBASE HTTPS page requests with HTTP/2.
  Only the public CA was mounted; neither client nor proxy disabled TLS checks.
  The configured 5s spacing was observed. This validates this deployment path,
  not that mitmproxy cures every 403.

## Native multiarch publishing follow-up

The first hosted run used upstream's single-runner QEMU build and spent several
minutes in ARM64 standard-library compilation. It was cancelled before publication.
At the user's request, image CI now uses separate native amd64/arm64 runners,
architecture-scoped caches and digest artifacts, then merges and verifies a
manifest only after both jobs succeed. Dockerfile build/target platform arguments
also allow efficient Go cross-compilation in local builds (`CGO_ENABLED=0` remains
set by the existing Makefile). The revised Dockerfile built both Linux amd64 and
arm64 images locally; both containers started and returned HTTP 200 with AVBASE
in `/v1/providers` (amd64 runtime used local emulation on this ARM host).
`actionlint` and `git diff --check` passed for the revised workflow.

## Limits

These are small real-site probes, not a sustained throughput/success-rate study.
403/429 recovery mechanics use local fixtures; no attempt was made to deliberately
trigger a live ban. AVBASE may still challenge/block a deployment's network path.
Optional external-store enrichment has fixture coverage; other live stores,
full actor aliases, media playback and CharmingReel ingestion were not tested.
No claim of universal proxy or anti-bot compatibility is made.

Sources: [MetaTube provider configuration](https://metatube-community.github.io/wiki/metadata-providers/),
[AVBASE access discussion](https://github.com/metatube-community/jellyfin-plugin-metatube/discussions/555),
[mitmproxy certificates](https://docs.mitmproxy.org/stable/concepts/certificates/).
