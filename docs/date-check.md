# The date check

The date check looks for Anime-Lists mappings that air dates contradict. For every AniDB episode an Anime-Lists node maps to TVDB, regular episodes and specials alike, it compares two dates. One is the episode's air date in the AniDB mirror. The other is the air date of the TVDB episode the node points to, in TVDB's official order. A node that fails gets an issue for a person to look at. The check never stops a build or a release, and no pull request waits for it.

## What raises a lead

A node is a lead when any of these holds:

- Its TVDB series is gone. SkyHook, the metadata service Sonarr uses, answers 404 for the series id.
- An episode it maps to does not exist on TVDB, for example an episode past the end of a season.
- An episode it maps to airs more than a day away from the AniDB episode. Another episode of the same series airs within a day of the AniDB date, and the issue lists it as the likely right target.
- A special it maps to airs more than a day away from the AniDB special, no TVDB special airs on the AniDB date, and the series keeps AniDB's calendar. A series keeps it when at least one regular AniDB date has a TVDB episode within a day. That date must be precise to the day and belong to one episode only.
- The node fails the order check, which reads the date mismatches the other rules leave undecided. That check looks for two AniDB episodes the node maps to one TVDB episode while an AniDB episode numbered between them lands after it. Episodes that air one after the other cannot go back to a TVDB episode already used. The issue lists the episodes involved as out of order.

The check reads a node the way the published file does. The overlay entries apply first. A mapping row that names the episode decides where it goes. Otherwise a regular episode goes to `defaulttvdbseason` at its number plus `episodeoffset`, and with `defaulttvdbseason="a"` to that absolute number. A special no row names goes to TVDB season 0 with the same number, which is how Anime-Lists reads it.

## What the check skips

Some dates cannot decide anything, so the check leaves them out. Each rule has a test in `internal/datecheck`.

- An episode with no air date on either side is checked only for whether its TVDB episode exists.
- An episode is checked from 7 days after its AniDB air date, because TVDB can add a new episode a few days late. An episode with no AniDB date is checked once AniDB records the anime's end date.
- A date on the 1st of a month matches any day of that month, and 1 January matches any day of that year. AniDB records some OVAs only to the month or the year. Such a date matches its target loosely, and points only to an episode within a day of it. It takes no part in the order check.
- For a regular episode, a date mismatch counts only when another episode of the series airs within a day of the AniDB date. A series whose dates never meet AniDB's follows another calendar, such as a dub's broadcast dates, and its dates cannot say which episode is which. Only the order check reads such a series. A node that only reorders its episodes passes it, because some series aired out of order.
- On a series that keeps AniDB's calendar, a regular episode with no TVDB episode on its date counts only through the order check. Checked by hand, 19 of 20 such episodes were the right episode, with TVDB dating it from a pre-air, a streaming release or a typo.
- A date that two regular episodes, or two specials, of the anime share points to no episode. A film split into parts or a batch of episodes released on one day cannot say which TVDB episode is which. The exception is a mapping row that sends one episode to two TVDB episodes. When one of them airs on the AniDB date, the other one counts as a mismatch.
- The order check leaves out TVDB season 0, because specials have no airing order.
- For a special, only a TVDB special counts as the other episode. A regular episode that airs on the day of a special is no evidence.
- Take a regular episode the node's default places in TVDB season 1 or later. It is skipped when its TVDB episode is at most 31 days away and another episode of that season airs on the AniDB date. Two sources that number a season alike and keep different broadcast schedules look exactly like that, for example after a pre-emption.
- A special no row names counts as missing only when another TVDB special airs on its date. TVDB not listing a special at all is no mapping error. Checked by hand, 18 of 20 such specials were not on TVDB at all. They were music videos, interviews, credit sequences and pre-broadcast specials.
- A node whose anime has no episode list in the AniDB mirror is not checked.

## Known limits

Two of the skip rules above can hide a wrong mapping. They stay because flagging those cases is mostly noise. On 2026-10-06, flagging a regular episode with no TVDB episode on its date would have added 686 issues. Flagging a special that TVDB does not list would have added 1,193. In a hand-checked sample of 20 from each group, 1 was a real mapping error. A node with such an episode still gets a lead when it fails another rule, and a regular episode of that kind still counts in the order check.

A date check cannot tell a wrong episode from the right episode dated differently. About 1 lead in 7 is the right episode, with AniDB and TVDB recording different air dates for it. Such a lead never passes on its own. A person who proves the mapping right can record the lead as reviewed, which keeps its issue closed, as [Marking a lead fine](#marking-a-lead-fine) explains.

## The issues

Each failing node gets one issue, labelled `lead:date-mismatch` and titled `lead: AniDB <id> ...`. It links the AniDB page and the TVDB official-order pages of the seasons involved, and shows the node as checked. A table lists each failing episode with both dates, what failed, and the TVDB episodes that air on the AniDB date. The issue holds episode numbers and dates only, never an AniDB or TVDB title.

A lead is not proof. To act on one, check the episodes against AniDB, TVDB's official order and TMDB, one by one. If the node is wrong, add an overlay entry that meets the bar in [overlay.md](overlay.md), and send the same rows to Anime-Lists in a pull request. The issue closes on its own once the node passes, whether Anime-Lists fixed it or an overlay entry did. If the node is right, record the lead as reviewed. An issue closed by hand opens again on the next run while the node still fails and no reviewed record covers it.

A run opens at most 50 new issues. It opens dead series first, then nodes with a missing TVDB episode, then date mismatches. The other leads wait for the next runs, so a long list arrives over several days. A run also edits at most 50 issues and closes at most 50, so a change that touches every issue spreads over several days too.

## Marking a lead fine

When the mapping is right and only the dates disagree, record the lead in `reviewed/`. Each record is one file named for the AniDB id, such as `reviewed/3195.json`. A lead its record covers gets no issue, and the daily run closes its open issue with a comment naming the record.

Air dates are what misled the check, so they prove nothing here. Compare the episodes by title and by order on AniDB, TVDB's official order and TMDB.

To write a record, run the date check locally as [How often it runs](#how-often-it-runs) shows, then run this from the repository root with the lead's AniDB id:

```sh
go run ./cmd/animap datecheck accept -datecheck datecheck.json -id 3195 -reason "<the episodes and their titles that prove the mapping>" -evidence https://anidb.net/anime/3195 -evidence https://thetvdb.com/series/kamichu/seasons/official/1
```

The reason is one paragraph that names the episodes and their titles on each side. Each `-evidence` is an https link to an AniDB, TVDB, TMDB, AniList or SeaDex page, with no user name or password in it, and at least one is required. The command refuses an id that is not a lead in that run, a dead series, an empty reason, any other link, and a record over 64 KiB. It never replaces an existing record unless you add `-force`. Commit the file it writes.

A record keeps the lead's findings, each a kind and an AniDB episode such as `date-mismatch:8` or `missing-target:S1`. It also keeps three SHA-256 fingerprints from that run:

- The node as checked, with the overlay applied. Titles and notes are left out, as in an overlay entry.
- TVDB's official order on every season the check can read for the node. When AniDB lists a regular episode, that is every season of the series, because the check looks on all of them for an episode that airs on the AniDB date. For an anime with only specials, it is the seasons the node maps onto and season 0, or every season for a node with `defaulttvdbseason="a"`. It covers each episode's number and air date, a digest of each season's titles, and whether SkyHook still serves the series.
- AniDB's episode list in the mirror: each episode's type, number, air date and titles, and whether AniDB records an end date.

A record covers its lead while all three fingerprints match the current run and every current finding is one the record lists. A finding that clears changes nothing. The lead is raised again when a fingerprint changes, such as after a title edit or once SkyHook stops serving the series. It is also raised again when an episode starts to fail once it is 7 days past its air date. Its issue opens again with one sentence naming the review date and what changed. Review the lead again, then fix the mapping or write a new record with `-force`. A lead never opens again for any other reason.

Pull request CI and `animap build` read every record. They refuse one that does not parse, is named for another id, or holds a malformed finding or fingerprint. A record whose node an overlay entry later changes stays valid, and its lead is raised again while the node still fails. A record whose node passes the check covers nothing and can be deleted.

## How often it runs

The check runs in the daily [drift workflow](../.github/workflows/drift.yaml) and checks every node each time. It reads the Anime-Lists file at its newest commit. It reads the AniDB mirror at the commit `.github/workflows/publish.yaml` pins, which Renovate moves on the 1st and 15th of each month. It keeps each TVDB series' episode numbers and dates, and a digest of each season's titles, between runs in the Actions cache.

A series is fetched again from SkyHook once its copy is over 6 days old, at one request a second. The copies one full read fetched come due together on the daily run a week later, so every series is read once a week. A run fetches for at most 90 minutes. A series it does not reach, or cannot read, keeps its older copy for later, and its nodes are not checked on that run, so their issues stay as they are.

On 2026-10-06 the list mapped 4,291 TVDB series for nodes the mirror lists. Reading all of them took 4,291 requests and 72 minutes, and the kept copy is 11 MB. That weekly refresh fits the workflow's 120-minute limit. On the other days a run fetches only series that are new to the list. A kept copy with episodes but no title digests is fetched again like a new series, because its fingerprint could never match a fresh copy's.

To run it yourself from the repository root, with Go and `curl`:

```sh
export CACHE_DIR=$PWD/.cache
export MIRROR_COMMIT=$(sed -n 's/^  MIRROR_COMMIT: //p' .github/workflows/publish.yaml)
curl -fsSLO https://raw.githubusercontent.com/Anime-Lists/anime-lists/master/anime-list-master.xml
go run ./cmd/animap datecheck -list anime-list-master.xml -mirror "$(bash scripts/fetch-mirror.sh)" -layouts .cache/layouts.json
```

It writes the leads and the counts to `datecheck.json` and opens no issue. `scripts/fetch-mirror.sh` keeps the snapshot under `.cache/mirror/` and extracts it again when the pinned commit or animap's extractor changes, so a local run reads the snapshot in the same form as the daily run. The first run reads every TVDB series once, so it takes as long as the weekly refresh.
