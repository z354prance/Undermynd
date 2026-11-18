# Rename: Undermynd → HyveMynd

Decided 2026-11-16. Started 2026-11-17 (roadmap I2) once the native engine was in place; status per step below.

Undermynd is growing from a download-and-transcode layer into one app that replaces Radarr, Sonarr, Lidarr, Prowlarr, Seerr, Houndarr and Recyclarr. The new name reflects that: many workers, one mind.

- Name: **HyveMynd** (display name). Lowercase `hyvemynd` for the repo, image and paths; `HYVEMYND_*` for environment variables.
- Logo direction (chosen 2026-11-16): concept 1, "hive planet". Keep the current purple sphere and htop-rainbow ring, and replace the "U" cutout with three hexagon cells. Concepts canvas: https://claude.ai/artifact/SSKKybjUqei8o7HezFKbEw
- Keep the same repository and evolve the code in place. Do not start a new repo.

## What stays named undermynd (on purpose)
- Files and folders inside `/config` and `/data` (`undermynd.db`, `backups/undermynd-*.db`, `.undermynd-replaced`), the binary path `/undermynd` and cookie names: renaming them would move user data or sign everyone out for no gain.

## Checklist for the rename
1. Tag the last pre-rename state (`v1.0-undermynd`, at main `e8de964`). **For the owner:** the build session cannot create tags; create it on GitHub (Releases → new tag on that commit).
2. Rename the GitHub repo to `hyvemynd`. **For the owner** (Settings → Repository name); GitHub redirects old links and remotes.
3. CI publishes both `ghcr.io/z354prance/undermynd` and `ghcr.io/z354prance/hyvemynd`. **Done** (I2).
4. `HYVEMYND_*` environment variables accepted; `UNDERMYND_*` still work and win when both are set. **Done** (I2).
5. New Unraid template `unraid/hyvemynd.xml` (old one kept). **Done** (I2); its icon is the current one until the final logo.
6. Display name, page titles, README, log prefix: **done** (I2). Final hive-planet logo, favicon and wordmark: with the design pass. Go module path: after step 2.
7. Its own release, separate from features or storage changes: I2 changes no stored data.
