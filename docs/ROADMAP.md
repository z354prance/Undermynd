# Roadmap: folding the arr stack into Undermynd (v46, Nov 18, 2026)

Goal: one app that does what Radarr, Sonarr, Lidarr, Prowlarr, Seerr, Houndarr and Recyclarr do today, so nothing else has to run or be configured. It will be renamed HyveMynd at the end.

How to use this doc: the **milestones table** is the plan. Each milestone is one or a few pull requests, has a clear exit test, and lists what it depends on. Decisions and gates explain why the order is what it is. This file is kept in the repo (`docs/ROADMAP.md`) and in the Undermynd project (`design/roadmap-arr-consolidation.md`) with the same content. Supporting project docs: `design/metadata-sources.md`, `design/anime-pipeline.md`; in the repo: `docs/RENAME.md`.

v13 restructured the plan into milestones. v14 to v17 answered every gate that blocks the start. v18 recorded Stage A progress. v19 recorded B1 merged and added B0 (Go upgrade). v20 closed A1. v21 closed B1 and B0. v22 recorded B2 merged. v23 recorded the B3 spike results. v24 recorded B4 (metadata layer) merged. v25 recorded C1 built. v26 recorded the C1 fixes and C2 built. v27 recorded that anime stays on the simple importer. v28 recorded C3 built. v29 recorded C4 built. v30 recorded E1 and F1 built. v31 recorded F2 built. v32 recorded F3 built. v33 recorded F4 built. v34 recorded F5 built. v35 recorded F6 built: Stage F is complete in code. v36 recorded G-1 built. v37 recorded G-2 built: Stage G is complete in code. v38 recorded H1 built. v39 recorded H2 built: Stage H is complete in code. v40 recorded I1 built. v41 records I2 built in code: the backend roadmap is complete; the design pass is next. v42 records design parts 1 to 4 merged.

## Rolled back (v43, Nov 18, 2026)

The user decided to keep HyveMynd beside the arr stack instead of replacing it: the takeover features did not work the way they expected. The redesign stays.

- **Kept:** everything up to and including the removal of the anime catalog (PR #22); the media-server interface (B5); the rename to HyveMynd (I2); the redesign: graphite look and icon-rail admin (design part 1), the public dashboard with its fixes (part 4), and the Seerr-style Request media dropdown, now backed by Seerr (part 3: Discover rows, poster search, title page with cast, trailer and a season picker; requests go to Seerr with the chosen seasons).
- **Removed, kept on the branch `archive/arr-takeover`** (main at `0b4c385`, PRs #23 to #51): anime follow and gap filling (C3, C4), indexers and the Torznab proxy (D1, D2), requests kept by HyveMynd (E1), the release parser, profiles, shadow mode, native movies and TV, wanted and calendar (F1 to F6), TRaSH sync and the scheduler (G-1, G-2), MusicBrainz and native music (H1, H2), API tokens and notifications (I1), the poster library pages (design part 2) and the unfinished admin pages (part 5).
- **Admin (design part 5, Nov 18):** a menu built for what remains: Overview (health pills, Needs you, Just finished, Pipeline now, Categories); Downloads (On the way with the segmented circle on each job, Feeds, Re-encode); Requests (Media: Seerr's waiting requests as poster cards; Accounts: the need-access queue); Library (Missing, see v46; Anime); Settings (Arrs, Download clients, Tdarr, Public page, Media server, Server, Log). Sign-in and first-run setup are one centred card. Settings pages use the settings-card pattern; on a phone the menu is a bottom bar and job rows become cards.
- **Database:** the schema keeps every migration up to 18, so a database that already ran the takeover build opens unchanged; the tables only the archived features used stay empty or unused. Settings fields those features added are ignored.
- **Brought back (v45, Nov 18):** the anime follow feature (C3, C4, and PRs #24 to #26), at the user's request: the Anime page under Library, following shows on nyaa with a group order, interactive nyaa search, and gap filling once a day. It never depended on the arr takeover; what had not lined up with the library's naming was the TMDB catalog (C1, C2), which stays removed. New with it:
  - **Placed files are named after their show folder**, in the way that folder already names its files: SxxEyy or a bare number, the same zero padding, the resolution in parentheses or a [Group Res] tag when the folder's files have one. A new folder follows the release's own form, Rename.ps1 style. A category's "keep release names" switch turns this off.
  - **Naming page** (Anime › Naming, and "Tidy names" on each show): lists folders whose older files do not fit, with a preview of every rename (subtitles follow their video; nothing is overwritten), files with no episode number to replace through a nyaa search, and episodes kept twice.
- **Missing page (v46, Nov 18):** Library › TV became Library › Missing: what each arr reports as wanted and not on disk, one section per arr category (Sonarr, Radarr, Lidarr colours), as a grid of posters taken from the arr (series and movie posters, album covers, falling back to the artist's picture) through the image proxy. Sonarr items are grouped per series with the missing episode count and codes; each poster opens the item in its arr. A filter box searches all sections.
- **To revisit:** check out `archive/arr-takeover`; the rest of this document describes that work as it was.

---

## 1. Decisions (settled)

| Topic | Decision |
|---|---|
| Audience | Personal, non-commercial, self-hosted, one household. TMDB free terms, with a Credits section. Each install brings its own TMDB key. |
| Requests | The public request box stays open: **no sign-in** (no Emby sign-in, no accounts). Anyone who can reach the page can search and request, as today. Every request waits in an approval queue on the admin dashboard; the admin approves or declines. Rate-limited per address. Optional free-text name so the admin knows who asked. An "auto-approve everything" setting, off by default. No quotas, no accounts. Request status visible on the public status page through the pipeline stages. |
| Metadata (G3, confirmed by B3) | TMDB is primary for structure (seasons, episodes, air dates) and alternate titles. The anime-offline-database supplements alternate titles (it filled a gap TMDB had) but is stale (last release 2026-07-04), so it is optional and cached. The Kometa Anime-IDs list maps AniDB to TVDB; TMDB's `/find` turns TVDB into TMDB. New shows not in any list: TMDB title search, confirmed once by the user. Where release seasons and TMDB seasons disagree, a per-series episode order (TMDB default, a TMDB episode group, or a season offset). Details in the project's `design/metadata-sources.md`. |
| Storage | SQLite with a pure-Go driver (`modernc.org/sqlite`, no cgo, static binary and scratch image kept) at `/config/undermynd.db`. Shipped in B1. `/config` is on the cache pool (confirmed). |
| What stays as files | `settings.json`, `admin.json`, `sessions.json`, `secret.key` stay as files: small, hand-editable, and sign-in recovery must not depend on the database. Everything else that is state (jobs, RSS seen items, tracked shows, access requests, requests, and all new catalog data) moves to SQLite. `storage-scan.json` is a cache and can stay. |
| Dependencies | The SQLite driver, and (since F2) `github.com/dlclark/regexp2`, a pure-Go regex engine with .NET rules: custom formats from the arrs and the TRaSH Guides use lookarounds, which Go's own regex cannot read. No others unless a later decision adds one. |
| Media server | Emby first, Jellyfin through the same small interface. Plex out of scope. Used for libraries, the existing access flow and optional refresh, not for signing in to make requests. |
| Library refresh | Optional, off by default. Emby finds new files on its own today. |
| Anime | First-class and native (no Sonarr), and **stays on the simple folder-name importer** (user decision, Nov 17): anime numbering does not line up with TMDB well enough to manage it by a catalog, which is why the user never let Sonarr handle it. Anime features work from the folders themselves: folder name, the episode numbers on disk, group preference. Mostly SubsPlease; per-show exceptions such as Trix and Judas. nyaa through per-release RSS feeds. 1080p global default. |
| Library numbering (G1) | The anime library is mixed (absolute and seasons) and stays as it is. New episodes follow the folder they land in, as today. No conversion tool. |
| Transcoding of AV1 sources | AV1 sources go through the same Tdarr flow and Undermynd already handles the result. Tdarr is never skipped: it does more than transcoding (user, Nov 17). |
| Arr import confirmation | Confirmed working against the real Sonarr and Radarr (user, Nov 16). |
| Rollout model | Strangler. Each category says who owns it: an arr (today's behaviour) or native. A category switches only after a shadow or trial period. The fake qBittorrent and SABnzbd APIs stay as a compatibility mode. |
| Merging | Each milestone is a pull request; CI (gofmt, vet, race tests, container build) must pass before merge. Merging to `main` publishes the container image. Milestones that change stored data (B1 onwards) are run on the server before the next one builds on them. |
| Name | HyveMynd, at the very end (`docs/RENAME.md`). Logo direction: concept 1, "hive planet". |
| Out of scope | Plex, Whisparr, Readarr/books, subtitles (Bazarr), user accounts for requests, quotas, multi-tenant hosting, importers for other people's arr setups. Revisit only if asked. |
| How the user wants to be involved | Cares that it works, not about implementation details. Bring only real choices and failures to the user; decide the rest and record it here. |

## 2. Where it is today (Nov 16, main at bfd7c01)

- About 12.6k lines of Go, standard library only, `go vet` clean, all tests pass with `-race`, including end-to-end runs against fake TorBox, Tdarr, NZBGet and arrs, and the new job store contract tests.
- Download and transcode layer: fake qBittorrent and SABnzbd APIs for the arrs; TorBox, local qBittorrent and NZBGet backends; job state machine with retries, reconciler, cleanup and import confirmation (confirmed on real arrs); Tdarr hand-off with progress, stall detection and hand-back of originals that need no work; resolution check that blocklists fakes; AV1 re-encode tab.
- Anime path with no arr: RSS feeds and rules, watch folder, `internal/library` parser and folder matcher, `placeInLibrary`. Trix, SubsPlease and Judas episodes place. (Judas file names inside the torrent are placed fine; the `{English title} [Uncensored]` form seen in the RSS titles is also handled since A1.) Absolute numbers parse but default to season 1.
- From Overmynd: public request box (no sign-in, rate-limited) backed by Seerr, with admin approve/decline; Emby/Jellyfin access flow; Tracearr playback; storage monitor; season progress (`internal/progress`, from the arrs).
- State (jobs, RSS seen items, tracked shows, access requests) is in SQLite since B1, with daily backups; old JSON files are imported once and kept as `.bak`. The TorBox limiter and storage scan stay as small files.
- CI starts the built container and checks that it creates the database and a backup.
- Built with Go 1.27 (current release) and `modernc.org/sqlite` v1.62.0 since B0.

## 3. Target shape

| Module | Job | Grows from |
|---|---|---|
| `db` | SQLite, schema migrations, backups | new |
| `catalog` | Movies, series, seasons, episodes, albums, files, aliases, releases; per-series numbering mode | new |
| `metadata` | TMDB, the offline anime database and ID mapping (later MusicBrainz), with cache, rate limiter, refresh | new |
| `mediaserver` | Emby/Jellyfin: libraries, users for the access flow, optional refresh | `access`, `playback` |
| `parser` | Release names to title, aliases, numbering, quality, group | `library.Parse` |
| `importer` | Place files into the library, never guess, never overwrite, dry-run; follows each series' numbering mode | `placeInLibrary` |
| `indexers` | Torznab, Newznab, nyaa; health, limits; unified search | `rss` |
| `decision` | Profiles, custom formats, rejection reasons | `pipeline/quality.go` |
| `scheduler` | Monitoring, missing, gap filling, upgrades, polite batches | `rss` poller, `progress` |
| `requests` | Public search and request (no sign-in), admin approval queue | `seerr`, `web/requests.go` |
| `pipeline` | Grab, download, transcode, verify (exists) | unchanged core |

Rule: modules talk through small interfaces so tests can use fakes, as the pipeline already does.

The catalog always stores both numberings for an episode (season/episode and absolute), whatever the folder layout. Matching, missing detection and gap filling work on the catalog. Only the importer and the conversion tool care about the layout on disk.

## 4. Definition of done (every milestone)

1. Its own pull request, with tests; `gofmt`, `go vet`, `go test -race` pass in CI.
2. New behaviour sits behind a setting or a per-category switch, and today's behaviour still works with it off.
3. Run for real on the Unraid server before the next milestone builds on it.
4. Anything skipped, rejected or unplaced says why, in the admin UI.
5. Rollback written down: which setting turns it off, and whether the database change can be undone.
6. README status table and this roadmap updated.
7. For milestones with UI: the screen is designed on the canvas first (see section 7).

Sizes: **S** one build session, **M** two to four, **L** about a week of sessions, **XL** several weeks. They are relative, not promises.

## 5. Milestones

### Stage A: Stabilise what exists
| ID | Milestone | Size | Depends on | Exit test | Status |
|---|---|---|---|---|---|
| A1 | Parser fix: strip `{...}` and descriptive tags like `[Uncensored]` from titles, keep braces text as an alias; match by title or alias; keep the episode number in the cleaned name; tests from real Trix, Judas, SubsPlease names | S | none | Judas names place in `Seitokai ni mo Ana wa Aru`; user runs Sweep on the anime category and checks whether the Student Council holes were unplaced files | **Done** (PR #2). Correction: the earlier test used RSS item titles, not the file names inside the torrents; the user confirmed Judas episodes were already importing fine. So the Student Council holes are episodes that were never grabbed, not stuck files: C4 (gap filling) is what fixes them |
| A2 | Job store contract tests (round trip, copies, order, update, reopen, pruning, crash safety, corrupt file, concurrency) | S | none | Tests pass against the current JSON store | **Done** (PR #3). Found and fixed a bug: `Job.Clone` shared `PastRemoteIDs` |
| A3 | Housekeeping: merge PR #1, refresh README status, add this roadmap to `docs/` | S | none | README matches the code | **Done** |
| A4 | Real-run checks | S | none | Arr import confirmation works on real Sonarr and Radarr; AV1 sources handled by the existing Tdarr flow | **Done** |

### Stage B: Foundations
| ID | Milestone | Size | Depends on | Exit test |
|---|---|---|---|---|
| B0 | Move to a supported Go version (1.27), then update the SQLite driver to its newest release | S | B1 | CI green; container smoke test passes; no behaviour change. **Done** |
| B1 | SQLite: driver, migration framework, job store backend behind the same interface; one-time import of `jobs.json`, RSS seen items, tracked shows and access requests, keeping `.bak` copies; WAL and busy timeout; daily `VACUUM INTO` backup to `/config/backups`, keep the last 7 | M | A2 | A2 contract tests and all pipeline tests pass on SQLite; restart mid-job resumes; a real TorBox + Tdarr grab completes; restoring a backup works. **Done** (PR #5). Real-run check passed Nov 16: import with `.bak` files, backup written, container restarted mid-transcode, jobs resumed, finished and were imported by Sonarr |
| B2 | Catalog schema: series (with numbering mode), seasons, episodes (season/episode and absolute), movies, files, aliases, releases, requests; jobs can point at an episode or movie | M | B1 | Unit tests; schema migration up from B1. **Merged (PR #10).** Real-run check: after updating, the log shows schema version 2 and a backup taken before the upgrade |
| B3 | Metadata spike to confirm G3 on Blue Box, Seitokai, One Piece, Boku no Hero Academia and a current SubsPlease show | S | none | **Done Nov 17** (run in CI on an unmerged spike branch). All 5 found on TMDB; alias coverage needs TMDB plus the offline database; mapping reaches TMDB through TVDB for 4 of 5; One Piece is numbered absolutely by TMDB; **Blue Box season 2 is part of TMDB season 1**, so per-series episode order is needed. The Emby check moves to C6 |
| B4 | Metadata layer: TMDB client (search, details, alternative titles, seasons, episode groups, `/find`), offline anime database import (optional, cached), Kometa ID mapping, cache tables, per-source limiter, background refresh, TMDB key setting, Credits page | M | B2, B3 | Add a show by TMDB id; aliases from TMDB and the offline database stored; both numberings stored; an episode group can be chosen; refresh picks up a new episode. **Merged (PR #12).** Also run against the real TMDB and lists in CI: all 5 test shows identified first by the right TMDB show, every feed title finds its show, Blue Box's "Seasons" group gives 25 + 12, One Piece `1158` is S23E1158, Hero Academia `90` is S5E2. Real-run check passed on the user's server: key accepted, both lists downloaded (22,108 titles, 17,050 ids) |
| B5 | Media-server interface extracted from `access` and `playback`; Emby and Jellyfin implementations; list libraries; optional refresh setting | S | none | Access flow and playback unchanged; Emby tests pass; Jellyfin marked untested if not run. **Built** (`internal/mediaserver`): access, playback and the new optional "tell the media server about new files" (off by default, with a path map) go through it |
| B6 | Settings screen rebuilt around categories, with the per-category owner switch (arr or native) | M | design | Every current setting reachable; switch exists but only "arr" is selectable until a native mode ships |

### Stage C: Anime, folder-based
The anime path stays the simple importer: a release goes to the folder its title names, by the number in its name. Nothing is lined up with TMDB.

| ID | Milestone | Size | Depends on | Exit test |
|---|---|---|---|---|
| C1 | Catalog import of the anime library | | | **Withdrawn Nov 17.** Built (PRs #13 to #21) and run on the real library (868 of 948 folders imported), but lining up anime folders with TMDB took too much hand work for a library whose naming varies. Removed in PR #22; migration 5 clears what it recorded. Kept from it: the release group and resolution in the parser, the looser reading of older file names (`S01E01-Title`, `Episode 7`), thousands separators in title comparison |
| C2 | Catalog-based placement | | | **Withdrawn Nov 17** with C1 (it needs the catalog). Never switched on: it ran only as a dry run beside the simple importer |
| C3 | Followed shows, arr-style (Anime page): every show folder listed with episodes on disk, gaps and AniList status (display only); follow a show with an ordered group preference and a quality; Undermynd checks nyaa for followed shows itself and grabs new episodes (a later group only when the first choice skipped one, or after a day); hand-made RSS rules leave a followed show's releases to it; other titles per folder, which the simple importer also accepts; interactive search with grab and warnings; search gaps by hand; Add New makes the folder and follows it | M | none | **Built** (`internal/shows`, migration 6). Real-run check: follow two shows that have hand-made feeds and watch a week of episodes arrive |
| C4 | Gap filling from the folder, by itself: once a day each followed show (with "fill gaps" on, the default) looks for the numbers missing between the episodes in its folder, from what its own search found and then by searching up to four missing episodes one by one; the first group of the order that has one at the chosen quality gets it (old gaps do not wait for a better group); never an episode on disk, already grabbed, or whose release failed | M | C3 | **Built** (migration 8). Real-run check: Student Council's holes filled by themselves |
| C5 | Skip the Tdarr round trip for AV1 sources | | | **Dropped Nov 17** (user): Tdarr does more than transcoding, so every file keeps going through it |

Notes: C3 and C4 search nyaa through RSS search URLs, so they do not wait for D1; D1 later absorbs it. The catalog and metadata layer (B2, B4) stay for movies, TV and requests, where TMDB lines up well. The season conversion tool (old C6) is dropped.

### Stage D: Indexers (Prowlarr parity)
| ID | Milestone | Size | Depends on | Exit test |
|---|---|---|---|---|
| D1 | Indexer module: Torznab, Newznab and a nyaa adapter; test, health, backoff, rate and grab limits, priority; unified search; interactive search screen. Anime search moves onto it | L | B1, design | Every indexer the user has in Prowlarr works; anime searches go through it. **Built (backend, minimal page)**: Torznab and Newznab indexers with test, health and backoff, daily limits, priority, one search across all, grab into the pipeline, import from Prowlarr (direct where Prowlarr shows the key, through Prowlarr otherwise). Prowlarr's own tracker definitions are still reached through Prowlarr: reaching them directly is the remaining step before Prowlarr can be turned off. Anime keeps nyaa through the Anime page |
| D2 | Torznab/Newznab proxy for the arrs | M | D1 | Arrs point at Undermynd instead of Prowlarr for two weeks; Prowlarr off. **Built**: on the download-client address and key, `/indexers/<n>` passes a search to one indexer (its key stays in Undermynd), `/indexers/all-torrent` and `/indexers/all-usenet` search them all at once |

### Stage E: Requests (Seerr parity, no sign-in)
| ID | Milestone | Size | Depends on | Exit test |
|---|---|---|---|---|
| E1 | Native request flow: public search and discover from the metadata layer (no sign-in, rate-limited per address as today, optional requester name); every request lands in an approval queue on the admin dashboard with approve and decline (with a reason); "already in the library" and "already requested" detection; an "auto-approve everything" setting, off by default; request status shown on the public status page through the pipeline stages; admin notified of new requests. Approved anime requests go to the native library; approved movie and TV requests still go to Radarr and Sonarr through their API (TMDB to TVDB mapping) until Stage F. The current Seerr-backed box stays available behind a setting until this is trusted | L | B4, B2, design | Seerr off for two weeks with requests and approvals working as before. **Built** (setting "kept by: Undermynd" under Public site; Seerr stays the default until switched): TMDB search and trending with availability from Radarr/Sonarr, requests in the `requests` table, approval queue unchanged, approve adds to Radarr/Sonarr (first root folder and profile) or follows anime on the Anime page when nyaa has exactly one match, decline with a reason, auto-approve setting, approved requests marked available when they arrive |

### Stage F: Movies and TV engine (Radarr and Sonarr parity)
| ID | Milestone | Size | Depends on | Exit test |
|---|---|---|---|---|
| F1 | Full release parser: quality, source, group, language, editions, multi-episode, season packs. Corpus built from the arrs' own history (thousands of real titles with what the arr decided), plus the arrs' parse endpoint as a reference while they still run (verify the endpoint) | L | A1 | Agrees with the arrs on the corpus above an agreed rate; every disagreement reviewed. **Built** (`internal/parser`): title, year, seasons, multi-episode, season and multi-season packs, daily, anime absolute and v2, quality by the arrs' own names, proper/repack/real, codec, 10-bit, HDR, audio and channels, editions, languages, streaming service, group, hash; 70 corpus names plus a fuzz test. The corpus check against the user's arrs is a button on the Indexers page ("Compare with the arr's history": up to 1,000 grabs, agreement rate and every difference) — real-run check: run it on the TV and movie categories and review the differences |
| F2 | Profiles, custom formats and decision engine with rejection reasons | L | F1, D1 | Unit tests on scoring. **Built** (`internal/decision`, migration 10, Profiles page): quality profiles with groups, cutoff, upgrades and score thresholds; custom formats with the arrs' condition kinds and rules (.NET patterns); quality sizes; every turned-down release says why; ranking in the arrs' order; import of profiles, formats and sizes from Sonarr or Radarr; "Try a profile" decides on a real indexer search or pasted names. Real-run check: import from both arrs, try a few titles, and compare the pick with what the arr would take |
| F3 | Shadow mode: for every arr grab, Undermynd works out what it would have picked and logs the comparison (gate G4) | M | F2 | Agreement report over two weeks; disagreements explained. **Built** (`internal/shadow`, migration 11; Profiles page → Shadow mode, off by default): every 10 minutes, each new Sonarr or Radarr grab is searched on Undermynd's indexers and decided with the profile of the same name; verdicts same, as good, Undermynd better, arr better, would refuse the arr's pick, nothing found, not compared; capped at 20 searches an hour by default. Real-run check: import profiles, turn it on, and read the report after two weeks |
| F4 | Movies native: import Radarr's library state, monitor, grab straight into the pipeline, importer with naming and dry-run; approved movie requests go straight to the native library | L | F3, G4 | Movie category switched to native; Radarr off for two weeks. **Built** (`internal/movies`, migration 12, Movies page): per-category "native" switch with the movie library folder; import of Radarr's library (monitored, profile by name, file and quality, paths moved to the library folder); every 15 minutes, missing movies (again after 12 h) and cutoff-unmet ones (after a day) are searched within an hourly budget and the best accepted release grabbed into the pipeline; failed downloads blocklisted; finished downloads imported under the naming settings, a replaced file set aside in `.undermynd-replaced` (never deleted), or only described in a dry run; interactive search with verdicts; add by TMDB; approved movie requests for a native category go straight to it. Real-run check: import, switch on with dry run, then live; Radarr's monitoring off for the category |
| F5 | TV native: same, plus season packs, daily shows, upgrades | XL | F4 | TV category native; Sonarr off for two weeks. **Built** (`internal/tv`, migration 13, TV page): per-category native switch; import of Sonarr's library (series, episodes in Sonarr's numbering, monitoring, profile, files; anime series left to the Anime page); new episodes and air dates from TMDB daily (known numbers kept); every 15 minutes recent releases on the indexers are matched to wanted episodes (Sonarr's RSS sync), and wanted episodes are searched within an hourly budget, a finished season as one season search; daily shows by air date; upgrades to the cutoff; failed downloads blocklisted; season packs and multi-episode files imported to every episode they hold under the naming settings, replaced files set aside, dry run; series page with seasons, episode monitoring and interactive search; add by TMDB; approved TV requests for a native category go straight to it. Real-run check: import, dry run, then live; Sonarr's monitoring off for the category |
| F6 | Calendar, wanted, cutoff unmet, history, manual search and manual import screens | M | F4, design | Reachable from admin. **Built** (Wanted page; `internal/history`, migration 14): missing and cutoff-unmet movies and episodes with a search button each; calendar of native episodes from a week ago to three weeks ahead; history of grabs, imports, failures, dry runs and replaced files; manual import of video files from the data, complete or library folders, each matched to its movie or episodes. Design pass still to come |

### Stage G: Profiles and scheduling
| ID | Milestone | Size | Depends on | Exit test |
|---|---|---|---|---|
| G-1 | TRaSH Guides importer (Recyclarr parity): preview diff, local overrides, scheduled refresh; profiles may carry a transcode target | M | F2 | A guide profile imported and used for a real grab. **Built** (`internal/trash`; Profiles page → TRaSH Guides): reads the guides repository (one zip download, kept an hour), lists its Radarr and Sonarr profiles and quality sizes, previews what applying would change, applies the chosen profiles with the formats they score and their scores (score sets included), keeps scores changed locally at the next sync, and syncs daily when asked. No transcode target in profiles: Tdarr handles every file (decision of Nov 17). Real-run check: apply a guide profile and see it used for a grab |
| G-2 | Scheduler (Houndarr parity): small batches, per-item cooldown, release grace window, global indexer budget; anime gap filling moves onto it | M | D1, F4 | Backlog search runs a week within the indexer budget. **Built** (`internal/scheduler`, migration 15; Wanted page → Scheduler): one hourly budget (30 by default) shared by native movie and TV searches, recent-release checks, anime gap-filling searches and the arr backlog; a grace window after an episode airs (2 h); the arr backlog search (off by default) asks Sonarr and Radarr categories that are not native for a few missing, then cutoff-unmet items each hour (5), each resting 7 or 14 days. Real-run check: turn the backlog on for a week and watch the budget hold |

### Stage H: Music (Lidarr parity)
| ID | Milestone | Size | Depends on | Exit test |
|---|---|---|---|---|
| H1 | MusicBrainz metadata with a 1 request/second limiter and cache | M | B4 | Artist and album lists load. **Built** (`internal/musicbrainz`): artist search and lookup, albums/EPs/singles, editions with track lists (usual edition first), Cover Art Archive covers; one request a second with retry on 503, responses cached in the database; admin API `/admin/api/musicbrainz/...`. The Music page comes with H2 |
| H2 | Music library, release matching, tagging, import | XL | H1, D1, G-1 | Lidarr off for two weeks. **Built** (`internal/music`, migration 16, Music page): per-category native switch; import of Lidarr's artists and albums with files, folder and quality; add an artist from MusicBrainz (studio albums monitored, EPs, singles and live albums not); new albums from MusicBrainz weekly; wanted albums (missing, partial, below the cutoff) searched within the shared budget; releases judged by artist, album, year, quality (FLAC 24bit to MP3-192, wanted list and cutoff in settings), seeders, discographies and the blocklist; finished albums moved to `<Artist>/<Album (Year)>/` with disc folders and covers, the old album set aside; dry run. **Decision:** files keep the tags their release carries; Undermynd does not rewrite tags (Emby reads them as they are). Real-run check: import from Lidarr, dry run, then live; Lidarr's monitoring off |

### Stage I: Finish
| ID | Milestone | Size | Depends on | Exit test |
|---|---|---|---|---|
| I1 | API tokens, webhooks, notifications | M | E1. **Built** (`internal/tokens`, `internal/notify`, migration 17; Connect page): API tokens (shown once, kept as hashes, revocable, last use shown) accepted as `X-Api-Key` or Bearer on every admin API except token management; notifications to Discord, ntfy or a JSON webhook for grabs, imports, failed downloads, new requests, replaced files and dry runs, with a test button and up to three tries |
| I2 | Rename to HyveMynd, following `docs/RENAME.md` | S | everything else | Old image and variables still work during transition. **Built in code** (see `docs/RENAME.md`): display name HyveMynd, both images published, `HYVEMYND_*` variables with `UNDERMYND_*` still working, new Unraid template. Left for the owner: the `v1.0-undermynd` tag and the GitHub repo rename (the build session cannot do either); then the Go module path. Final logo with the design pass |

## 6. Order and dependencies

```
A1 A2 A3 A4 (done; A1 waits on the user's Sweep check)
A2 -> B1 -> B2 -> B4           (C1, C2 withdrawn)
C3 -> C4                         anime, from the folders
B2 + B4 -> E1 -> [Seerr off]
B1 -> D1 -> D2 -> [Prowlarr off]
A1 -> F1 -> F2 -> F3 -> F4 -> F5 -> [Radarr, Sonarr off]
             F2 -> G-1        D1 + F4 -> G-2
B4 -> H1 -> H2 -> [Lidarr off]
everything -> I2 (rename)
```

Recommended sequence for one builder: C3 → C4 → B5 → D1 → E1 → D2 → F1 to F6 → G → H → I. B6 (settings screen) can go anywhere after B1, but before the first category switch. C5 and C6 whenever wanted.

## 7. Design track

The design pass follows the pinned designs: `design/pinned-v2-hyvemynd-dashboard.md`, `-admin.md`, `-phone.md`, and `design/pinned-v3-posters-and-requests.md` (library posters and Seerr-style requests).

| Part | What | Status |
|---|---|---|
| 1 | Theme (graphite, Barlow, IBM Plex Mono, self-hosted), icon rail with five sections, logo and favicon | **Merged** (#45) |
| 2 | Library as poster grids with the name below; movie, show, artist and album pages; art through the `/img` proxy | **Merged** (#46) |
| 3 | Request media, Seerr-style in the dropdown: Discover rows, poster search, title page with cast, trailer and season picker | **Merged** (#47) |
| 4 | Public dashboard: Now Playing, On the way with segmented circles, Recently added, storage ring, Donate, Need access; old page at `/classic` | **Merged** (#47) |
| 5 | Remaining admin pages: Overview health and Needs you, Requests cards, Calendar week grid, Settings cards, Categories under Connections, Notifications and API under Settings, sign-in card, phone layouts | Next |

The earlier plan below listed screens each UI milestone needed; parts 1 to 5 cover them.

| Screen | Needed by |
|---|---|
| Settings with per-category owner switch | B6 |
| Anime: followed shows, group preference, generated feeds, gaps found in folders | C3, C4 |
| Indexers and interactive search | D1 |
| Public request page (search, discover, request, status) and admin approval queue | E1 |
| Wanted, calendar, history | F6 |
| Shadow-mode report | F3 |

## 8. Gates (decisions that block a milestone)

| Gate | Question | Blocks | Status |
|---|---|---|---|
| G1 | How are long-running shows laid out? | C3, C4 | **Answered:** mixed, mostly absolute, and staying that way: anime is handled by folder, not by catalog (Nov 17). |
| G2 | Who makes requests and how are they approved? | E1 | **Answered:** no sign-in; every request waits for admin approval on the dashboard. |
| G3 | Where does show information come from? | B4, E1, F | **Answered:** TMDB for structure, anime-offline-database for anime alternate titles, community ID mapping; confirmed by the B3 spike. |
| G4 | Shadow agreement good enough to trust native picks for movies | F4 | Later |
| G5 | Before turning any arr off: two weeks of that category on native with no manual fixes | every "off" | Later |

## 9. Testing strategy

- Keep the existing fakes (TorBox, Tdarr, NZBGet, arrs) and add fakes for TMDB, the offline anime database, nyaa and Emby.
- The job store contract tests (A2) run unchanged against every store backend.
- Release-name corpus as table tests, grown from real feeds and arr history; every misparse found in use becomes a test.
- Placement tests for both numbering modes, and for a series switching mode after conversion.
- Request flow tests: public request with no sign-in, rate limit, duplicate and already-available detection, approve, decline, auto-approve setting, status updates.
- Shadow mode before any native grab decision goes live.
- Dry-run for the importer and the conversion tool before either writes to the library; conversion undo tested.
- Database migrations tested up from every earlier schema version.
- Real-run checklist on Unraid at the end of every milestone.

## 10. Risks and how the plan handles them

| Risk | Handling |
|---|---|
| Misparsed or misplaced files | Never guess, never overwrite; dry-run; "needs a person" queue; corpus tests (A1, C2, F1) |
| Mixed numbering in the library | Per-series mode, both numberings in the catalog, mixed series sent to a person (C1) |
| Conversion breaking a big show or Emby watch history | Optional and per series, preview, undo log, rename only, small show first, Emby check in B3 before C6 |
| Breaking the working download path | Store contract tests (A2), same interface, backups and `.bak` files (B1), per-category switch |
| Open request box abused (spam requests) | Nothing downloads without admin approval; per-address rate limit; auto-approve off by default |
| Metadata gaps and numbering mismatches | Spike before building (B3), aliases, absolute mapping, unmapped shows confirmed once by the user |
| Offline anime database or ID mapping stops updating | Cached copy keeps working; AniList fallback for new shows; manual match in the queue |
| Native decisions worse than the arrs' | Shadow mode and gate G4 |
| Indexer abuse or bans | Per-indexer limits and a global budget (D1, G-2) |
| Scope creep across seven apps | Out-of-scope list; no accounts or quotas; opinionated settings with an Advanced section |
| Shared-folder permissions | Work and complete folders stay configurable; document PUID/PGID |
| Jellyfin drift | Behind the media-server interface, labelled untested until run |

## 11. Change log

- v46: Library › Missing: per-arr poster grid of what the arrs report missing, artwork from the arr.
- v45: anime follow, nyaa search and gap filling back; placed files named after their folder; Naming page.
- v44: design part 5 for the rolled-back admin: new menu, restyled pages, sign-in card, phone layout. No Anime tab in Request media.
- v43: rolled back to beside-the-arrs; the takeover features are kept on `archive/arr-takeover`. The redesign stays, with requests through Seerr.
- v42: design parts 1 to 4 merged.
- v13: milestones with sizes, dependencies and exit tests; store tests before SQLite; which state moves to SQLite; backups in B1; anime search through nyaa RSS search URLs; per-category owner switch and shadow mode; corpus from the arrs' own history; dropped importers for other people's setups; design track and gates.
- v14: G1 answered: per-series numbering mode, both numberings stored, optional season conversion tool (C6).
- v15: Tdarr/AV1 answered (C5 optional).
- v16: requests need no sign-in; every request waits for admin approval. Stage E is one milestone. A4 done.
- v17: G3 answered: TMDB plus anime-offline-database plus ID mapping.
- v18: A1 merged (PR #2), A2 done (PR #3, Clone bug fixed), A3 done (README status, this file added to `docs/`). Merge rule recorded.
- v41: I2 built in code (rename). Backend complete; design pass next, from the pinned v2 designs (`design/pinned-v2-hyvemynd-*.md`).
- v40: I1 built: API tokens and notifications.
- v39: H2 built: native music; tags are not rewritten.
- v38: H1 built: MusicBrainz client.
- v37: G-2 built: shared search budget, grace window, arr backlog search.
- v36: G-1 built: TRaSH Guides sync.
- v35: F6 built: Wanted page (missing, cutoff unmet, calendar, history, manual import).
- v34: F5 built: native TV categories.
- v33: F4 built: native movie categories.
- v32: F3 built: shadow mode beside the arrs, with a report on the Profiles page.
- v31: F2 built: profiles, custom formats, decision engine; regexp2 added as a dependency for .NET patterns.
- v30: E1 built (requests kept by Undermynd, Seerr still the default until switched). F1 built (full release parser, with a comparison against the arrs' grab history). Also since v29: B5 (media-server interface), D1 (indexers) and D2 (Torznab/Newznab proxy for the arrs) merged.
- v29: C5 dropped (Tdarr is never skipped). C4 built: followed shows fill their gaps once a day. Since v28 also: folder scans kept between restarts (migration 7), episodes being processed shown in purple, failed downloads reopen the episode, nyaa queries without leading hyphens.
- v28: C3 built: the Anime page (arr-style), followed shows checked on nyaa, group order with fallback, other titles for the importer, AniList status shown.
- v27: anime stays on the simple folder-name importer (user decision). C1 and C2 withdrawn and removed (PR #22, migration 5 clears their records); C3 and C4 now work from the folders; C6 dropped.
- v26: C1 real scan of 948 folders: fixes for unchecked folders counted as waiting, older file names (`S01E01-Title`), readings ranked by files with their own episode, exact-title matches for shows the lists lack, thousands separators, list ties settled by title, extra episodes past a season's end. C2 built, starting in a dry run.
- v25: B4 real-run check passed. C1 built: folder import with a confirm queue, per-show numbering and episode order picked by how well each fits the files, half-hourly folder check, Library anime tab and show page. Group per episode shows for files Undermynd places from now on (C2); files already in the library have names without the group.
- v24: B4 merged: `internal/metadata`, migration 3, Metadata settings tab, `/credits`. The real-source check found a new show whose release spelling only an AniList-only list entry had; list entries without an AniDB id now contribute titles when they share one with TMDB.
- v23: B3 done: spike results recorded; G3 refined (TMDB primary for titles too, offline database optional, mapping through TVDB, per-series episode order); B4 and C1 widened accordingly.
- v22: B2 merged: migration 2 and `internal/catalog` (series, episodes with both numberings, movies, aliases, files, missing and season counts, job targets, requests table). Migration test from every earlier version added.
- v21: B1 done (real-run check passed), B0 done (Go 1.27, SQLite driver v1.62.0). Also fixed: usenet grabs listed twice in Sonarr (PR #7).
- v20: A1 closed. Lesson recorded: test parsing against the file names inside a release, not only feed titles.
- v19: B1 merged (PR #5): SQLite store, state blobs, backups, container smoke test in CI. Added B0: move to a supported Go version. The build machine cannot reach the Go module proxy, so new dependencies are resolved by a temporary CI job.

## 12. Housekeeping

- Prior art to read before Stage F: Arrmada and Arrgo, both Go all-in-one arr replacements.

## References
- Project docs: design/metadata-sources.md, design/anime-pipeline.md, design/redesign-v1plus.md
- Repo: https://github.com/z354prance/Undermynd (rename notes: `docs/RENAME.md`)
- Logo concepts: https://claude.ai/artifact/SSKKybjUqei8o7HezFKbEw
- Houndarr: https://av1155.github.io/houndarr/docs/concepts/how-houndarr-works
- Seerr: https://docs.seerr.dev/blog/seerr-release/
- Recyclarr: https://next.recyclarr.dev/wiki/features/
- Prior art: https://www.opensourcealternatives.to/item/arrmada, https://forums.unraid.net/topic/197066-support-arrgo-arr-stack-alternative-written-in-go-htmx-media-management/
