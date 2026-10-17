# The date check

The date check looks for Anime-Lists mappings that air dates contradict. For every regular episode and special an Anime-Lists node maps to TVDB or TMDB, it compares two dates. One is the episode's air date in the AniDB mirror. The other is the air date of the episode the node points to, in TVDB's official order or in TMDB's episode order. A credit, promotion, parody or other episode is checked only where a mapping row or an overlay link the build applies names it, and since the mirror keeps no date for those kinds, only for whether its target exists. A node that fails is a lead for a person to look at, in the anime's issue of the [daily review](review.md). The check never stops a build or a release, and no pull request waits for it.

Both sides follow the same rules below, with TMDB in place of TVDB. A node's TMDB side reads `tmdbtv`, `tmdbseason`, `tmdboffset` and the rows' `tmdbseason`. TMDB numbers no episode absolutely. A TMDB movie the node names in `tmdbid` is checked only for whether TMDB still serves it: Anime-Lists maps no episode onto a movie, so there is nothing to compare.

## What raises a lead

A node is a lead when any of these holds:

- Its show is gone. SkyHook, the metadata service Sonarr uses, answers 404 for the TVDB series id, or TMDB answers 404 for the TMDB show id. On TMDB, the same goes for each movie id in `tmdbid`.
- An episode it maps to does not exist on TVDB, for example an episode past the end of a season.
- An episode it maps to airs more than a day away from the AniDB episode. Another episode of the same series airs within a day of the AniDB date, and the issue lists it as the likely right target.
- A special it maps to airs more than a day away from the AniDB special, no TVDB special airs on the AniDB date, and the series keeps AniDB's calendar. A series keeps it when at least one regular AniDB date has a TVDB episode within a day. That date must be precise to the day and belong to one episode only.
- The node fails the order check, which reads the date mismatches the other rules leave undecided. That check looks for two AniDB episodes the node maps to one TVDB episode while an AniDB episode numbered between them lands after it. Episodes that air one after the other cannot go back to a TVDB episode already used. The issue lists the episodes involved as out of order.

The check reads a node the way the published file does. The overlay entries apply first. A mapping row that names the episode decides where it goes. Otherwise a regular episode goes to `defaulttvdbseason` at its number plus `episodeoffset`, and with `defaulttvdbseason="a"` to that absolute number. A special no row names goes to TVDB season 0 with the same number, which is how Anime-Lists reads it. Where no `episodeoffset` is written, or a special has no row, the published file flags the target `default`.

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
- A special no row names counts as missing only when another TVDB special airs on its date. TVDB not listing a special at all is no mapping error. Checked by hand, 18 of 20 such specials were not on TVDB at all. They were music videos, interviews, credit sequences and pre-broadcast specials. A special an applied overlay link names counts as missing whenever TVDB lacks its target, since a published entry points there.
- A node whose anime has no episode list in the AniDB mirror is not checked against a show, though its TMDB movies still are.

## Known limits

Two of the skip rules above can hide a wrong mapping. They stay because flagging those cases is mostly noise. On 2026-10-06, flagging a regular episode with no TVDB episode on its date would have added 686 issues. Flagging a special that TVDB does not list would have added 1,193. In a hand-checked sample of 20 from each group, 1 was a real mapping error. A node with such an episode still gets a lead when it fails another rule, and a regular episode of that kind still counts in the order check.

A date check cannot tell a wrong episode from the right episode dated differently. About 1 lead in 7 is the right episode, with AniDB and TVDB recording different air dates for it. Such a lead never passes on its own. A person who proves the mapping right can accept it, which keeps its section closed, as [Accepting a finding](review.md#accepting-a-finding) explains.

## The issue sections

A failing node's TVDB and TMDB findings each get a section in the anime's one issue, as [review.md](review.md) describes. A section links the AniDB page and the provider's pages of the seasons involved: TVDB's official-order pages, or TMDB's show and season pages. The issue shows the node as checked once. A table lists each failing episode with both dates, what failed, and the provider's episodes that air on the AniDB date. The issue holds episode numbers and dates only, never an AniDB, TVDB or TMDB title.

A lead is not proof. To act on one, check the episodes against AniDB, TVDB's official order and TMDB, one by one, by title and by order: the dates are what misled the check, so they prove nothing. If the node is wrong, add an overlay entry that meets the bar in [overlay.md](overlay.md), and send the same rows to Anime-Lists in a pull request. The section clears on its own once the node passes, whether Anime-Lists fixed it or an overlay entry did. If the node is right, [accept the finding](review.md#accepting-a-finding).

## How often it runs

The check runs in the daily review and checks every node each time. It reads the Anime-Lists file at its newest commit. It reads the AniDB mirror at the commit `.github/workflows/publish.yaml` pins, which Renovate moves on the 1st and 15th of each month. It keeps each show's episode numbers, air dates and a digest of each episode's title between runs in the Actions cache, one file per provider, and never a title.

A show the review cannot read on a run, because its fetch failed or the budget ran out, keeps its older copy for later, and its nodes are not checked on that run, so their sections stay as they are.

- **TVDB**, through SkyHook at one request a second. A series is fetched again once its copy is over 6 days old, so the copies one full read fetched come due together on the daily run a week later. Every series an overlay entry captures is fetched on every run, because overlay drift compares it. A run fetches SkyHook for at most 90 minutes.
- **TMDB**, through the TMDB API at four requests a second, a tenth of TMDB's limit. One request reads a show with its seasons 0 to 19. Each show and movie is fetched again on its own day of the week, the remainder of its id divided by 7, so no run reads all of them. One a run missed is fetched once its copy is over 8 days old. A run fetches TMDB for at most 60 minutes, at the same time as SkyHook.

The TMDB side needs a read access token in the `TMDB_TOKEN` repository secret. Without one, no TMDB section is checked, never cleared, and the summary issue says so. A token TMDB refuses fails the run after the issue plan applies.

On 2026-10-16 the list mapped 4,285 TVDB series, 4,214 TMDB shows and 1,577 TMDB movies for the nodes the review reads. Reading all of them from empty copies took 4,285 SkyHook requests in 72 minutes and 5,798 TMDB requests in 24 minutes, side by side. A daily run after that read the 865 series overlay entries capture in 14 minutes, and a seventh of the TMDB ids, 835 requests, in 4 minutes. On the weekly day every TVDB series comes due again, about 72 minutes, which fits the workflow's 120-minute limit. The kept copies are 6.2 MB for TVDB and 6.0 MB for TMDB, against a 64 MiB bound each.

To run the check yourself, see [Running it yourself](review.md#running-it-yourself). A first run reads every show once, so it takes as long as the weekly TVDB refresh.
