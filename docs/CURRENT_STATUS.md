# Current status

This fork includes AVBASE in standard server builds. The adapter reads the
structured data in public pages, paces metadata requests and handles temporary
failures with bounded retries/cooldowns. Optional per-provider proxy support uses
normal TLS verification. Existing metadata/database identities are preserved.

Image CI builds amd64 and arm64 on separate native runners and merges their
digests into shared tags only after both builds succeed.

The native matrix build and manifest publication succeeded for `d30bfb28ff27`.
Verified image: `ghcr.io/charmingcheung/metatube-server:sha-d30bfb28ff27`
(also tagged `dev`), supporting Linux amd64 and arm64.

Deployment/configuration: [avbase.md](avbase.md).
Actual verification and live-test limits: [avbase-report.md](avbase-report.md).
