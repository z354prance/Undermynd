<p align="center"><img src="assets/undermynd-logo.svg" width="160" alt="Undermynd"></p>

# Undermynd

One container that owns the whole download-to-library path for Sonarr, Radarr and Lidarr.

- Acts as their **qBittorrent** download client, so a grab keeps the same download ID from request to import.
- Hands the grab to a local qBittorrent (TorBox, NZBGet/SABnzbd, Tdarr and RSS are the next phases), keeps only the media files, moves them atomically into the complete folder, confirms the arr's import, then cleans up everything it created.
- A public status page for your users, and a sign-in-protected admin side, in one app on one address.
- Standard library only. One static binary of about 7 MB, about 7 MB of memory at idle.

## Status

| Phase | Scope | State |
|---|---|---|
| 1 Core | Job table and state machine, fake qBittorrent API, local qBittorrent backend, staging, hand-back, import confirmation, cleanup, status page, admin sign-in | **built and tested** |
| 2 TorBox | Adapter, persisted rate limiter, batched polling, verified pull, tracked delete | not started |
| 3 Transcode | Tdarr adapter, verification gate, in-flight cap | not started (a category with *transcode* on fails the grab with a clear reason) |
| 4 Usenet | Fake SABnzbd, NZBGet backend | not started |
| 5 RSS | Feeds and rules | not started |
| 6 Hardening | Notifications, API tokens, more admin actions | partly (reconciler, retries and cleanup are in) |
| 7 Overmynd merge | Requests, access requests, playback | not started |

The design is in `docs/`.

## Run

Use the Unraid template in `unraid/undermynd.xml`, or:

```
docker run -d --name undermynd -p 8080:8080 -p 8081:8081 \
  -v /mnt/user/appdata/undermynd/config:/config -v /mnt/user/data:/data \
  -e PUID=99 -e PGID=100 ghcr.io/z354prance/undermynd:latest
```

1. Read the container log. On first start it prints the **download client login** for the arrs and a **setup key**.
2. Open `http://host:8080/admin`, enter the setup key, choose a password, scan the authenticator secret, confirm a code, and save the recovery codes.
3. Under *settings*: set each category's arr URL and API key (the arr that confirms the import), and the local qBittorrent URL and login.
4. In Sonarr, Radarr and Lidarr add a **qBittorrent** download client: host = this machine, port = **8081**, the login from step 1, and the category (`radarr`, `tv-sonarr`, `lidarr`).

Mount the same `/data` in Undermynd, qBittorrent, the arrs and Tdarr, on one cache-only share, so every move is a rename.

## Ports

| Port | Purpose | Exposure |
|---|---|---|
| 8080 | Public status page, and the admin pages under `/admin` behind a password and TOTP | Reverse proxy, open to users |
| 8081 | Download client API for the arrs | LAN only. Never expose it. |

Behind a reverse proxy set `UNDERMYND_TRUST_PROXY=true` so the real client IP is used for sign-in throttling and the origin check.

## Folders

```
/data/undermynd/client/<job>/     local qBittorrent saves here (removed after the media is staged)
/data/undermynd/staging/<job>/    media files only
/data/complete/<category>/<release>/   what the arr imports from; removed after the import is confirmed
```

## Security notes

- The public side reads only a public view (title, state group, progress). No paths, IDs, reasons or settings.
- Every admin route and API call passes one session check before any handler runs. State-changing calls also need a custom header and a same-origin check.
- Sign-in needs the password **and** a TOTP code (or a one-time recovery code), with backoff on repeated failures. The error never says which part was wrong.
- Stored secrets (arr API keys, qBittorrent password, the client login) are encrypted with AES-256-GCM using a key in `/config/secret.key`, and no API returns them.
- First-run setup needs the setup key from the log, so nobody who merely reaches the page can claim the account.
- Passwords use PBKDF2-HMAC-SHA256 (600,000 rounds), from the Go standard library.

## Known gaps

- The arr history check (`downloadId` + import event) is written from the arr API as documented and tested against a stand-in; confirm it against your real Sonarr, Radarr and Lidarr. Set `UNDERMYND_LOG_REQUESTS=1` to log every call the arrs make to the fake client and compare.
- The job file is a small JSON file written atomically after every change, not SQLite, because this build uses only the standard library.
- The release folder in `complete/` is named after the release (not the job ID) so the arrs can parse it.

## Branding

Logo files are in `assets/`; the Unraid icon is `unraid/undermynd-icon.png`.
