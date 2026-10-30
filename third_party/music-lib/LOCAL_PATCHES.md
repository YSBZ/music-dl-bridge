# Local provider fixes

Snapshot: github.com/guohuiyuan/music-lib v1.1.1-0.20261018010056-7cc4467b4e30.
License: AGPL-3.0, original LICENSE retained. This is a repository-owned Go module
replacement; never edit the user's sources/ snapshot or the shared module cache.

Changes:
- kuwo/kuwo.go: request high-resolution FLAC, FLAC, 320k MP3, then 128k MP3.
- netease/netease.go: prefer hires before lossless for automatic selection.
- netease/{netease,download}.go: reject freeTrialInfo responses in both URL paths.
- Regression tests in kuwo/download_quality_test.go and netease/download_quality_test.go.
- migu/{download,migu}.go: prefer FLAC over WAV, then MP3, in catalogue and download selection; download_test.go covers this order.
- qq/song.go: expose the catalogue's available_format (FLAC/MP3) for format ranking; search_quality_test.go covers it. This is availability metadata, not a guarantee of the current account's entitlement.

Preserve explicit caller quality settings. Account entitlements remain enforced
by the upstream service. The application independently validates downloaded audio.
Update this snapshot against upstream before removing or expanding these patches.
