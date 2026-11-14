<p align="center"><img src="assets/undermynd-logo.svg" width="160" alt="Undermynd"></p>

# Undermynd

One container that owns the whole download-to-library path for Sonarr, Radarr and Lidarr.

- Acts as their **qBittorrent** and **SABnzbd** download client, so a grab keeps the same download ID from request to import.
- Downloads through **TorBox**, or hands off to local qBittorrent / NZBGet when TorBox is not configured.
- Sends video to **Tdarr** (music skips it), verifies the result, moves it into the complete folder, confirms the arr import, then cleans up everything it created.
- Built-in **RSS auto-download**, modeled on qBittorrent's, replacing a separate RSS tool.
- A public status page for your users (progress, recently added, now playing, requests) and a sign-in-protected admin side, in one app.

Status: design stage. The design write-up lives in `docs/` once exported; the first build target is the core pipeline for Radarr.

## Install on Unraid

Template: `unraid/undermynd.xml` (raw URL: `https://raw.githubusercontent.com/z354prance/undermynd/main/unraid/undermynd.xml`). Add it as a template repository or paste the URL when adding a container.

The image is published to `ghcr.io/z354prance/undermynd` on every push to `main` once a `Dockerfile` exists.

## Ports

| Port | Purpose | Exposure |
|---|---|---|
| 8080 | Public status page, and the admin pages under `/admin` behind a password and TOTP | Reverse proxy, open to users |
| 8081 | Download client API for the arrs | LAN only |

## Branding

Logo files are in `assets/`; the Unraid icon is `unraid/undermynd-icon.png`.
