# Roadmap: folding the arr stack into Undermynd (v25, Nov 17, 2026)

Goal: one app that does what Radarr, Sonarr, Lidarr, Prowlarr, Seerr, Houndarr and Recyclarr do today, so nothing else has to run or be configured. It will be renamed HyveMynd at the end.

How to use this doc: the **milestones table** is the plan. Each milestone is one or a few pull requests, has a clear exit test, and lists what it depends on. Decisions and gates explain why the order is what it is. This file is kept in the repo (`docs/ROADMAP.md`) and in the Undermynd project (`design/roadmap-arr-consolidation.md`) with the same content. Supporting project docs: `design/metadata-sources.md`, `design/anime-pipeline.md`; in the repo: `docs/RENAME.md`.

v13 restructured the plan into milestones. v14 to v17 answered every gate that blocks the start. v18 recorded Stage A progress. v19 recorded B1 merged and added B0 (Go upgrade). v20 closed A1. v21 closed B1 and B0. v22 recorded B2 merged. v23 recorded the B3 spike results. v24 recorded B4 (metadata layer) merged. v25 records C1 built.

---

## 1. Decisions (settled)

| Topic | Decision |
|---|---|
| Audience | Personal, non-commercial, self-hosted, one household. TMDB free terms, with a Credits section. Each install brings its own TMDB key. |
| Requests | The public request box stays open: **no sign-in** (no Emby sign-in, no accounts). Anyone who can reach the page can search and request, as today. Every request waits in an approval queue on the admin dashboard; the admin approves or declines. Rate-limited per address. Optional free-text name so the admin knows who asked. An "auto-approve everything" setting, off by default. No quotas, no accounts. Request status visible on the public status page through the pipeline stages. |
| Metadata (G3, confirmed by B3) | TMDB is primary for structure (seasons, episodes, air dates) and alternate titles. The anime-offline-database supplements alternate titles (it filled a gap TMDB had) but is stale (last release 2026-07-04), so it is optional and cached. The Kometa Anime-IDs list maps AniDB to TVDB; TMDB's `/find` turns TVDB into TMDB. New shows not in any list: TMDB title search, confirmed once by the user. Where release seasons and TMDB seasons disagree, a per-series episode order (TMDB default, a TMDB episode group, or a season offset). Details in the project's `design/metadata-sources.md`. |
| Storage | SQLite with a pure-Go driver (`modernc.org/sqlite`, no cgo, static binary and scratch image kept) at `/config/undermynd.db`. Shipped in B1. `/config` is on the cache pool (confirmed). |
| What stays as files | `settings.json`, `admin.json`, `sessions.json`, `secret.key` stay as files: small, hand-editable, and sign-in recovery must not depend on the database. Everything else that is state (jobs, RSS seen items, tracked shows, access requests, requests, and all new catalog data) moves to SQLite. `storage-scan.json` is a cache and can stay. |
| Dependencies | The SQLite driver is the only new dependency unless a later decision adds one. |
| Media server | Emby first, Jellyfin through the same small interface. Plex out of scope. Used for libraries, the existing access flow and optional refresh, not for signing in to make requests. |
| Library refresh | Optional, off by default. Emby finds new files on its own today. |
| Anime | First-class and already native (no Sonarr). Mostly SubsPlease; per-show exceptions such as Trix and Judas. nyaa through per-release RSS feeds. 1080p global default. |
| Library numbering (G1) | The library is mixed: most long-running shows are laid out with absolute numbers, some by season. Seasons are preferred but not required. Each series records its own numbering mode; new episodes follow it; new series default to seasons. Converting an existing series is optional and per series (C6). |
| Transcoding of AV1 sources | AV1 sources go through the same Tdarr flow and Undermynd already handles the result. Skipping Tdarr for them is only an optional speed-up (C5). |
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
| B5 | Media-server interface extracted from `access` and `playback`; Emby and Jellyfin implementations; list libraries; optional refresh setting | S | none | Access flow and playback unchanged; Emby tests pass; Jellyfin marked untested if not run |
| B6 | Settings screen rebuilt around categories, with the per-category owner switch (arr or native) | M | design | Every current setting reachable; switch exists but only "arr" is selectable until a native mode ships |

### Stage C: Native anime (first native category)
| ID | Milestone | Size | Depends on | Exit test |
|---|---|---|---|---|
| C1 | Library import: scan the anime library, match each folder to TMDB with the user confirming, detect each series' numbering mode and episode order from its files (absolute or seasons; TMDB default order, an episode group, or a season offset, as with Blue Box), sending anything unclear to a person, and record which episodes already exist | M | B4 | Season cells show real counts for anime, for absolute and season-laid-out shows alike, including Blue Box season 2. **Built (this PR):** `internal/animelib`, Library anime tab, show page, Import anime folders screen (designs on the C1 canvas). Real-run check: scan the library, confirm, compare season cells with the folders |
| C2 | Catalog-based placement: match by alias instead of folder name; place each episode in the series' own mode; "needs a person" queue with reasons and a manual resolve action; dry-run | M | C1, A1 | Name corpus passes; an absolute show and a season show each get new episodes in their own layout; nothing ever overwritten |
| C3 | Monitoring: monitor a series, ordered group preference per show, 1080p default; generate per-show nyaa search feeds through the existing RSS poller | M | C2 | Two of the user's hand-made feeds replaced with generated ones for a week with no misses |
| C4 | Missing and gap filling: per-episode search from the next group when one skips; air dates stop early "missing"; never re-download an episode another group already supplied; group and resolution shown per episode | M | C3 | Student Council holes filled automatically |
| C5 | Optional speed-up: skip the Tdarr round trip when the source already matches the target codec | S | none | A Trix AV1 release is placed without a Tdarr run |
| C6 | Season conversion tool, optional and per series: preview every rename and move, convert on confirm, write an undo log, refresh that show in Emby, then switch the series' mode to seasons. Hardlink or rename only; never overwrite; stop on anything unmapped | M | C2, B3, B5 | One small absolute show converted, checked in Emby, and undone once; then a mid-size show. One Piece only when the user chooses |
| Switch | Anime category set to native | | C1 to C4 | Two weeks without manual fixes |

Notes: C3 and C4 search nyaa through RSS search URLs, so they do not wait for D1; D1 later absorbs it. C5 and C6 are not on the critical path.

### Stage D: Indexers (Prowlarr parity)
| ID | Milestone | Size | Depends on | Exit test |
|---|---|---|---|---|
| D1 | Indexer module: Torznab, Newznab and a nyaa adapter; test, health, backoff, rate and grab limits, priority; unified search; interactive search screen. Anime search moves onto it | L | B1, design | Every indexer the user has in Prowlarr works; anime searches go through it |
| D2 | Torznab/Newznab proxy for the arrs | M | D1 | Arrs point at Undermynd instead of Prowlarr for two weeks; Prowlarr off |

### Stage E: Requests (Seerr parity, no sign-in)
| ID | Milestone | Size | Depends on | Exit test |
|---|---|---|---|---|
| E1 | Native request flow: public search and discover from the metadata layer (no sign-in, rate-limited per address as today, optional requester name); every request lands in an approval queue on the admin dashboard with approve and decline (with a reason); "already in the library" and "already requested" detection; an "auto-approve everything" setting, off by default; request status shown on the public status page through the pipeline stages; admin notified of new requests. Approved anime requests go to the native library; approved movie and TV requests still go to Radarr and Sonarr through their API (TMDB to TVDB mapping) until Stage F. The current Seerr-backed box stays available behind a setting until this is trusted | L | B4, B2, design | Seerr off for two weeks with requests and approvals working as before |

### Stage F: Movies and TV engine (Radarr and Sonarr parity)
| ID | Milestone | Size | Depends on | Exit test |
|---|---|---|---|---|
| F1 | Full release parser: quality, source, group, language, editions, multi-episode, season packs. Corpus built from the arrs' own history (thousands of real titles with what the arr decided), plus the arrs' parse endpoint as a reference while they still run (verify the endpoint) | L | A1 | Agrees with the arrs on the corpus above an agreed rate; every disagreement reviewed |
| F2 | Profiles, custom formats and decision engine with rejection reasons | L | F1, D1 | Unit tests on scoring |
| F3 | Shadow mode: for every arr grab, Undermynd works out what it would have picked and logs the comparison (gate G4) | M | F2 | Agreement report over two weeks; disagreements explained |
| F4 | Movies native: import Radarr's library state, monitor, grab straight into the pipeline, importer with naming and dry-run; approved movie requests go straight to the native library | L | F3, G4 | Movie category switched to native; Radarr off for two weeks |
| F5 | TV native: same, plus season packs, daily shows, upgrades | XL | F4 | TV category native; Sonarr off for two weeks |
| F6 | Calendar, wanted, cutoff unmet, history, manual search and manual import screens | M | F4, design | Reachable from admin |

### Stage G: Profiles and scheduling
| ID | Milestone | Size | Depends on | Exit test |
|---|---|---|---|---|
| G-1 | TRaSH Guides importer (Recyclarr parity): preview diff, local overrides, scheduled refresh; profiles may carry a transcode target | M | F2 | A guide profile imported and used for a real grab |
| G-2 | Scheduler (Houndarr parity): small batches, per-item cooldown, release grace window, global indexer budget; anime gap filling moves onto it | M | D1, F4 | Backlog search runs a week within the indexer budget |

### Stage H: Music (Lidarr parity)
| ID | Milestone | Size | Depends on | Exit test |
|---|---|---|---|---|
| H1 | MusicBrainz metadata with a 1 request/second limiter and cache | M | B4 | Artist and album lists load |
| H2 | Music library, release matching, tagging, import | XL | H1, D1, G-1 | Lidarr off for two weeks |

### Stage I: Finish
| ID | Milestone | Size | Depends on | Exit test |
|---|---|---|---|---|
| I1 | API tokens, webhooks, notifications | M | E1 | |
| I2 | Rename to HyveMynd, following `docs/RENAME.md` | S | everything else | Old image and variables still work during transition |

## 6. Order and dependencies

```
A1 A2 A3 A4 (done; A1 waits on the user's Sweep check)
A2 -> B1 -> B2 -> B4 -> C1 -> C2 -> C3 -> C4 -> [anime native]
B3 ------------^             C2 + B5 -> C6 (optional, any time after)
                             C5 (optional, any time)
B2 + B4 -> E1 -> [Seerr off]
B1 -> D1 -> D2 -> [Prowlarr off]
A1 -> F1 -> F2 -> F3 -> F4 -> F5 -> [Radarr, Sonarr off]
             F2 -> G-1        D1 + F4 -> G-2
B4 -> H1 -> H2 -> [Lidarr off]
everything -> I2 (rename)
```

Recommended sequence for one builder: C1 → B3 → B4 → C1 to C4 → B5 → D1 → E1 → D2 → F1 to F6 → G → H → I. B6 (settings screen) can go anywhere after B1, but before the first category switch. C5 and C6 whenever wanted.

## 7. Design track

The redesign doc says Settings, Library and Requests/Access are not designed yet. Each UI milestone needs its screen first, in the existing htop style:

| Screen | Needed by |
|---|---|
| Settings with per-category owner switch | B6 |
| Library: series list, show page with season cells, group and resolution per episode, numbering mode shown | C1, C4 |
| "Needs a person" queue with reasons and resolve actions | C2 |
| Season conversion preview (every rename listed, confirm, undo) | C6 |
| Indexers and interactive search | D1 |
| Public request page (search, discover, request, status) and admin approval queue | E1 |
| Wanted, calendar, history | F6 |
| Shadow-mode report | F3 |

## 8. Gates (decisions that block a milestone)

| Gate | Question | Blocks | Status |
|---|---|---|---|
| G1 | How are long-running shows laid out? | C1, C2 | **Answered:** mixed, mostly absolute; seasons preferred. Per-series mode, optional conversion (C6). |
| G2 | Who makes requests and how are they approved? | E1 | **Answered:** no sign-in; every request waits for admin approval on the dashboard. |
| G3 | Where does show information come from? | B4, C6 | **Answered:** TMDB for structure, anime-offline-database for anime alternate titles, community ID mapping; confirmed by the B3 spike. |
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

- v13: milestones with sizes, dependencies and exit tests; store tests before SQLite; which state moves to SQLite; backups in B1; anime search through nyaa RSS search URLs; per-category owner switch and shadow mode; corpus from the arrs' own history; dropped importers for other people's setups; design track and gates.
- v14: G1 answered: per-series numbering mode, both numberings stored, optional season conversion tool (C6).
- v15: Tdarr/AV1 answered (C5 optional).
- v16: requests need no sign-in; every request waits for admin approval. Stage E is one milestone. A4 done.
- v17: G3 answered: TMDB plus anime-offline-database plus ID mapping.
- v18: A1 merged (PR #2), A2 done (PR #3, Clone bug fixed), A3 done (README status, this file added to `docs/`). Merge rule recorded.
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
