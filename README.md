<p align="center"><img src="assets/undermynd-logo.svg" width="160" alt="Undermynd"></p>

# Undermynd

One container that owns the whole download-to-library path for Sonarr, Radarr and Lidarr.

- Acts as their **qBittorrent** download client, so a grab keeps the same download ID from request to import.
- Hands the grab to TorBox, a local qBittorrent or NZBGet (usenet arrives through a fake SABnzbd), sends videos through a Tdarr hand-off, keeps only the media files, moves them atomically into the complete folder, confirms the arr's import, then cleans up everything it created.
- Anime with no arr at all: RSS feeds and a watch folder feed the same pipeline, and finished episodes are placed straight into your show folders.
- A public status page with requests, for your users, and a sign-in-protected admin side, in one app on one address.
- Standard library only. One static binary of about 7 MB, about 7 MB of memory at idle.

## Status

| Area | Scope | State |
|---|---|---|
| Core | Job table and state machine, fake qBittorrent API, local qBittorrent backend, staging, hand-back, import confirmation, cleanup, status page, admin sign-in | built, tested, in daily use |
| TorBox | Usenet and torrents (limiter, batched polling, resumable pull, tracked delete) | built, tested, running against real TorBox |
| Transcode | Folder hand-off to a dedicated Tdarr library, progress, stall detection, verification gate | built, tested, running against real Tdarr |
| Usenet | Fake SABnzbd, TorBox usenet backend, NZBGet fallback | built and tested; TorBox usenet in real use; NZBGet tested against a stand-in only |
| Import confirmation | Arr history check (`downloadId` + import event) | confirmed against real Sonarr and Radarr |
| RSS and watch folder | Feeds, regex rules, first-poll baseline, dedupe, manual download, watch folder | built, in real use |
| Anime library | Places episodes in show folders by name, never guesses or overwrites; handles SubsPlease, Trix and Judas-style names | built, in real use |
| Re-encode | Converts library files to AV1 through the Tdarr flow | built |
| Overmynd merge | Requests (through Seerr), access requests, playback, storage | built |
| Hardening | Reconciler, retries, cleanup, log viewer | partly (notifications and API tokens not yet) |

**Where it is going:** Undermynd is growing into one app that replaces Radarr, Sonarr, Lidarr, Prowlarr, Seerr, Houndarr and Recyclarr, and will be renamed HyveMynd at the end (`docs/RENAME.md`). The plan is in `docs/ROADMAP.md`. Current stage: A (stabilise), then B (SQLite and a title catalog).

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

- The arr history check is confirmed against Sonarr and Radarr; Lidarr is untested. Set `UNDERMYND_LOG_REQUESTS=1` to log every call the arrs make to the fake client.
- The job file is a small JSON file written atomically after every change. It moves to SQLite in roadmap milestone B1.
- The release folder in `complete/` is named after the release (not the job ID) so the arrs can parse it.

## Branding

Logo files are in `assets/`; the Unraid icon is `unraid/undermynd-icon.png`.

## Usenet (SABnzbd side)

Add Undermynd to Sonarr/Radarr/Lidarr as a **SABnzbd** client on the machine port (host 18081 by default).
The **API key is the client password** (shown once in the log on first run, rotatable in the admin page).
Usenet goes to TorBox when a token is set, otherwise to NZBGet when its address is set.

## Tdarr hand-off

For categories with transcode on, finished videos go to the Tdarr **input folder** and Undermynd collects the result from the **output folder**. Create a dedicated Tdarr library that reads the input folder and writes folder-to-folder into the output folder. Undermynd never touches any other library.

With a Tdarr address (and API key, if Tdarr has authentication on) and the library's ID set on the Transcode tab, Undermynd also:
- asks Tdarr to scan just that job's folder, so timing does not depend on a folder watcher;
- shows the node, worker and percentage while a worker holds the file (`get-nodes`, and `cruddb` file records, the same calls Overmynd uses);
- fails the job at once when Tdarr marks the file errored, and treats a worker with no progress for the stall time as failed;
- keeps at most N jobs with Tdarr at once (default 6); the rest wait, which is not a failure;
- hands back the original when Tdarr decides a file needs no work.

Per category, a failed transcode either fails the grab (default, so the arr blocklists and searches again) or keeps the original.
The output is accepted only when it has stopped growing, passes a size check and has a valid container header. The design asked for an ffprobe duration check too, which needs ffprobe in the image and is not built yet.
Without a Tdarr address everything still works from the folders alone, but failures are only noticed when the timeout runs out.

## Folder layout

By default everything lives under the one `/data` mount: work files in `/data/undermynd`, hand-back folders in `/data/complete/<category>`.
If your layout differs, set `UNDERMYND_WORK` (staging, Tdarr hand-off, local client) and `UNDERMYND_COMPLETE` (what the arrs import from).
Keep both inside one mount of one cache-only share so every move is a rename.


## Log

Admin → Log shows the last 2000 lines of the container log since Undermynd started, with a filter box (a job id, `tdarr`, `failed`) and a follow switch. The one-time setup key is never kept in it.

## Resolution check

When a download finishes, Undermynd reads the real frame size of each video from its container header and asks the arr (Sonarr or Radarr, through the download's history) what quality it grabbed the release as. If the video is below that step (2160, 1080, 720, 480), such as a release labelled 1080p that is really 480p, the job fails and the arr is told to blocklist the release and search again. Wider-than-tall crops count by width, so a 1920x800 film is 1080p. If the arr cannot say, cannot be reached, or the frame size cannot be read, the download goes on as normal. Turn it off under Admin → System → Timing ("check resolution against the arr").

## Re-encoding files already in the library

The admin **Re-encode** tab lists videos in `Movies`, `Episodes` and `Anime` under the media share (`UNDERMYND_STORAGE_ROOT`, mounted at `/media`) that are not AV1 and arrived since a date you choose. The codec is read from the file's own header, so nothing needs ffprobe. Picked files are copied to staging, sent through the same Tdarr flow as a new grab, and the result is checked (valid container, read back as AV1) before it is written beside the original and moved over it. If Tdarr fails, produces anything that is not AV1, or the library file changes meanwhile, the library file is left as it was. These jobs never touch Sonarr or Radarr and are not shown on the public page; run a rescan in Sonarr or Radarr afterwards so it picks up the new files.

This is the only thing that writes to the media share, so the share has to be mounted read-write.
