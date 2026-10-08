# Current status

This fork includes AVBASE in standard server builds. The adapter reads the
structured data in public pages, paces metadata requests and handles temporary
failures with bounded retries/cooldowns. Optional per-provider proxy support uses
normal TLS verification. Existing metadata/database identities are preserved.

Image CI builds amd64 and arm64 on separate native runners and merges their
digests into shared tags only after both builds succeed.

The initial native matrix build and manifest publication succeeded for `d30bfb28ff27`.
Verified image: `ghcr.io/charmingcheung/metatube-server:sha-d30bfb28ff27`
(also tagged `dev`), supporting Linux amd64 and arm64.

A subsequent HTTP contract change exposes source cooldowns as 503 + Retry-After
with a structured retry time, including empty Auto searches. Coordinated client
backoff needs an image built with this change; the initial image above predates it.

Deployment/configuration: [avbase.md](avbase.md).
Actual verification and live-test limits: [avbase-report.md](avbase-report.md).
