# Pending rename: Undermynd → HyveMynd

Decided 2026-11-16. Not done yet, on purpose: the rename is clean-up work for the end of the arr consolidation roadmap, after the native engine is in place.

Undermynd is growing from a download-and-transcode layer into one app that replaces Radarr, Sonarr, Lidarr, Prowlarr, Seerr, Houndarr and Recyclarr. The new name reflects that: many workers, one mind.

- Name: **HyveMynd** (display name). Lowercase `hyvemynd` for the repo, image and paths; `HYVEMYND_*` for environment variables.
- Logo direction (chosen 2026-11-16): concept 1, "hive planet". Keep the current purple sphere and htop-rainbow ring, and replace the "U" cutout with three hexagon cells. Concepts canvas: https://claude.ai/artifact/SSKKybjUqei8o7HezFKbEw
- Keep the same repository and evolve the code in place. Do not start a new repo.

## Until then
- Keep using `undermynd` everywhere: repo, image, Unraid template, `UNDERMYND_*` variables.
- Do not add new code or config that hard-codes the new name.

## Checklist for the rename
1. Tag the last pre-rename state (for example `v1.0-undermynd`).
2. Rename the GitHub repo to `hyvemynd`. Check that old links and git remotes redirect.
3. CI publishes both `ghcr.io/z354prance/undermynd` and `ghcr.io/z354prance/hyvemynd` for a transition period. The container image does not follow a repo rename.
4. Accept `HYVEMYND_*` environment variables, and keep `UNDERMYND_*` working as aliases.
5. New Unraid template (`unraid/hyvemynd.xml`) and icon; keep the old template until the switch.
6. Logos and branding (final versions of the hive-planet mark: icon, favicon, Unraid icon, wordmark) in `assets/`, page titles, README, docs, log prefix, Go module path.
7. Do the rename as its own release, separate from any feature or storage change.
