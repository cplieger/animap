# The daily review

Once a day, the [Daily review workflow](../.github/workflows/daily-review.yaml) checks every anime in five ways and keeps one GitHub issue per anime with anything to say:

- **TVDB dates** compares AniDB's air dates with where each episode lands in TVDB's official order. [date-check.md](date-check.md) has the rules.
- **TMDB dates** makes the same comparison against TMDB's episode order, and checks that every TMDB movie id still exists.
- **Season 0 leftovers** pairs the AniDB specials and other episodes that land on no TVDB or TMDB episode with the season 0 episodes no anime claims on the same show. [Season 0 leftovers](#season-0-leftovers) has the rules.
- **Overlay drift** checks that the Anime-Lists node, the TVDB order and the AniDB episodes an [overlay](overlay.md) entry or link rests on have not moved.
- **SeaDex watch** checks that every [SeaDex](https://seadex.moe) entry is mapped well enough for seadex-scout to find it. [watch.md](watch.md) has the rules.

The run writes everything it found to `review.json`, plans the issue changes from it, and applies them. `review.json` is kept as a workflow artifact for 7 days.

## One issue per anime

An issue belongs to the file that fixes it:

| Key | Covers |
| --- | --- |
| `anime:anidb:<id>` | Both date checks, the season 0 leftovers and the overlay entry of AniDB node `<id>`, and the watch gap of every published AniList entry that is that whole anime, unless an overlay link the build applies names the entry's AniList id |
| `anime:anilist:<id>` | The overlay link `overlay/links/<id>.json`, and the watch gap of an AniList entry that is not one whole anime or that an applied link names |

The key sits in a marker at the end of the issue body. The title names the anime and the checks that disagree, for example `anime: AniDB 3195 Kamichu!, TVDB and TMDB dates, season 0 leftovers, overlay drift`. An issue kept open only by a check that could not run names that check instead, for example `anime: AniDB 3195 Kamichu!, TMDB dates not checked`. Every issue carries the labels `animap` and `anime`. Anime that share one TVDB series keep one issue each, and a dead series names in each issue the other anime it raised.

The body holds one section per check. A check the run could not do, because a source was down, a fetch budget ran out or the `TMDB_TOKEN` secret is missing, keeps its section exactly as the last run wrote it. A check that has nothing to say drops its section. A section with no earlier text says "not checked this run" and why.

An issue closes once every section in it was checked on the run and is clear. The closing comment says why each section cleared: the cause went away, or an acceptance record covers it. Closing an issue by hand does not stick, because the next run opens it again while a check still raises something.

A run opens or reopens at most 50 issues, edits at most 50 and closes at most 50, so a change that touches many anime spreads over several days. It opens issues in this order: overlay drift, a dead TVDB series, an episode TVDB lacks, other TVDB date findings, watch gaps, season 0 leftovers on TVDB, and then anime only TMDB disagrees with, season 0 leftovers on TMDB alone included. The rest wait for a later run.

## Season 0 leftovers

AniDB's episode list is the reference for what exists, and TVDB and TMDB for where it lands. The date check judges each AniDB episode against the episode its mapping points to. This check looks at the episodes that point nowhere. On each show an anime maps to, it lists:

- the AniDB specials and other episodes that land on no episode of the show: a row maps them to `0`, a row or a special's Anime-Lists number points to an episode the show lacks, no row names an other episode (Anime-Lists gives only specials a default; on TVDB an applied overlay link naming one lands it on its Anime-Lists number), or no Anime-Lists number addresses them
- the show's season 0 episodes that no AniDB episode of any anime on the show claims, as the collision guard reads the claims

It raises the section when both lists hold something, since one may be the other. An unclaimed episode joins the second list only when it could be one of the first: it has no date, or an other episode lands nowhere (the AniDB mirror dates none), or an undated special lands nowhere and the episode aired within a month of the anime's dated episodes, or AniDB dates none of them, or a dated special lands nowhere and the two dates can be one airing, by the date check's rule: at most a day apart, or in the same month when either date is the 1st (a month-precision date), or in the same year when either is 1 January (a year-precision date). On 2026-10-18, without those date limits, 648 anime raised the section, and 2 of 15 drawn at random and checked by title held a real match; nearly every other pair was a sequel's special, a later film or the provider's own extra. With them, 271 raise it, 231 on TVDB and 70 on TMDB, and 5 of 15 drawn at random held a real match.

Credits, promotions and parodies never raise the section: openings, endings, trailers and commercials are nearly never on TVDB or TMDB. When the section is raised, it lists the anime's promotions that land nowhere too, because a promotion can be a prologue. It names AniDB episode kinds and numbers, the show and its season 0 numbers, never a title, and links the show's season 0 page.

The section reads the layouts the date checks keep and makes no request of its own. Its source is not checked while either date check is not; a node is still judged on each show the run read, and a section that raises names any show it did not. A show whose layout the run could not refresh is not checked either. A section whose every raised show an acceptance part covers stays not checked while another of its shows is, so its issue does not close on half the anime.

Map an episode that is one of the unclaimed episodes with a row on the node's [overlay](overlay.md) entry, and send the row to Anime-Lists. A row moves it; an [overlay link](overlay.md#entries-filed-under-another-anime) moves nothing, and on TVDB lands a special on the node's own target and an other episode or promotion no row names on its Anime-Lists number. So a link maps an other episode or promotion when an AniList entry is that episode and the unclaimed episode carries its Anime-Lists number. An episode no Anime-Lists number addresses has no row that can map it yet: when it is one of the unclaimed episodes, accept the pair `inexpressible`, naming the format change in `requires`. If no episode that lands nowhere is any unclaimed one, accept the pair `fine`. Both take `-part leftovers`.

## The summary issue

One pinned issue, "Daily review: summary", shows:

- whether each check ran, and why not when it did not, with a notice at the top while TMDB is not checked
- how many anime each check raises
- the acceptance parts to delete, because their check is clear without them
- the anime waiting for an issue over the run's limit
- how many date and leftovers sections an acceptance part covered on this run, by verdict, and every accepted watch gap with its reason, as the old unmappable list did. A date or leftovers part's reason names episode titles, so it stays in its file.

## Accepting a finding

Some findings are right about the data and wrong about the anime: the dates disagree but the episodes are the same, or no source carries the work at all. A person who proves that records it in `reviewed/`, one file per anime named for its key, such as `reviewed/anidb-3195.json` or `reviewed/anilist-1563.json`. Each file holds one part per check it rules on:

| Part | Verdicts |
| --- | --- |
| `date-tvdb`, `date-tmdb` | `fine`: the mapping is right and the dates misled the check. `inexpressible`: the right mapping is known and the published format cannot say it |
| `leftovers` | `fine`: no episode that lands nowhere is one of the unclaimed episodes. `inexpressible`, as above |
| `watch` | `unmappable`: no source carries the work. `inexpressible`, as above |

An `inexpressible` part names the format change it needs in `requires`, such as `a per-episode TVDB series in the published schema`. A dead series can never be accepted, as `fine` or `inexpressible`: the node moves to the series that replaced it. A dead TMDB movie can never be `fine`. Overlay drift has no part: re-capturing the entry or link with `animap overlay capture` or `capture-link` acknowledges it.

To accept, download the `review` artifact of the latest Daily review run, or explain the anime locally, then run from the repository root:

```sh
go run ./cmd/animap accept -from review.json -key anime:anidb:3195 -part date-tvdb -verdict fine \
  -reason "<the episodes and their titles that prove the mapping>" \
  -evidence https://anidb.net/anime/3195 -evidence https://thetvdb.com/series/kamichu/seasons/official/1
```

For season 0 leftovers, pass `-part leftovers` and name in the reason, by title on each side, why no episode that lands nowhere is an unclaimed one. For a watch gap, add `-anilist <id>` when the anime has more than one watched entry. The reason names the episodes and their titles on each side, or the search that found nothing. Each `-evidence` is an https link to an AniDB, TVDB, TMDB, AniList or SeaDex page. The command refuses a key or part the report does not raise, a verdict the part does not take, an empty reason, any other link, and a file over 256 KiB. It never touches an existing file unless you add `-force`, which adds the part or replaces it. Commit the file it writes.

Pull request CI and `animap build` read every record and refuse one that does not parse or is named for another key.

## What keeps an acceptance honest

A part covers only what it was written against:

- A date part keeps, for each finding it accepted, a SHA-256 of what that finding rests on. That is the TVDB series or TMDB show it was judged on, the AniDB episode with its kind, number, air date and a digest of its titles, and every target the node gives it, with that episode's number, absolute number, air date and title digest, or that it is missing. It is also whether the node's default placed it, the link naming it, the episodes that air within a day of the AniDB date, and whether the provider still serves the show. A dead show's finding rests on the show's id alone.
- A leftovers part keeps, for each show it accepted, a SHA-256 of the show's id, the AniDB specials, other episodes and promotions that land nowhere on it, each with its kind, number and Anime-Lists number, and the unclaimed season 0 numbers on its list. It moves when either list changes, and never on a title. A provider re-dating a season 0 episode can move it, since the dates decide which unclaimed episodes join the list.
- A watch part keeps the gaps it accepted and a SHA-256 of what the watch reads of the published entry: its `type`, the TVDB series, TMDB movie and IMDb ids its parts reach, and, for each of its episodes in order, the AniDB episodes that episode is, each with its anime, kind and number and where it lands on TVDB. One episode made of several AniDB episodes and the same AniDB episodes as separate episodes digest apart. A change to the entry's other ids, its episode count, a TMDB placement or an episode of the anime the entry is not does not move it.

A part stops covering its section when one of those digests moves, or when a finding or gap it does not list appears. A finding that clears changes nothing. The section then comes back with one sentence saying which part was written and why it no longer covers the finding. Review it again, then fix the mapping or accept it again with `-force`.

A part whose check is clear without it shows in the summary as one to delete.

## Explaining one anime

`animap explain` prints, for one key, the issue the anime would get, its season 0 leftovers included, and every AniDB episode of every kind with where the node puts it on TVDB and on TMDB, each target's date and title digest, and every other node or link that claims the same target. Each episode carries its AniDB number, and a credit, promotion, parody or other episode its Anime-Lists season 0 number too, or none when no Anime-Lists number addresses it, so it lands nowhere. With `-json` it adds, for each show those nodes map to, every node on it and every episode with its claims, the published entries of the anime (`-release`) with anime-offline-database's AniDB ids for them (`-aod`), and the published anime rows of its nodes and of the AniDB ids those entries name that the anime's own nodes are not, with their episodes, so an AniList entry no node carries can still be traced:

```sh
export CACHE_DIR=$PWD/.cache
go run ./cmd/animap explain -key anime:anidb:3195 -list anime-list-master.xml \
  -mirror "$(MIRROR_COMMIT=$(sed -n 's/^  MIRROR_COMMIT: //p' .github/workflows/publish.yaml) bash scripts/fetch-mirror.sh)" \
  -layouts-tvdb .cache/layouts-tvdb.json -layouts-tmdb .cache/layouts-tmdb.json -fetch
```

`-fetch` reads that anime's TVDB and TMDB shows first and writes no layout file. TMDB needs `TMDB_TOKEN` in the environment. `-review review.json` takes the sections from a daily run's report instead of judging them now. `-json` prints the same report as `review.json`, with the episode detail added, and `animap accept -from` reads it.

## Running it yourself

`DRY_RUN=1 bash scripts/daily-review.sh` runs the whole review, reads the live issue list, and writes `review.json` and `actions.json` without changing any issue. `DRY_RUN` unset or empty applies the plan, and any value but `0` or `1` is read as a dry run. It needs `gh`, `curl`, `jq` and Go, and reads `TMDB_TOKEN` from the environment.
