# mapread

mapread gives a Go program the anime id mapping in animap's `animap.json`: which AniDB episodes an AniList entry is, which episodes AniDB lists with their AniDB episode ids, where each lands on TheTVDB, and which TMDB movie it is.

It reads, checks and downloads the version 2 file, and answers lookups over it in memory. It depends on two small libraries by the same author, [jsoncap](https://github.com/cplieger/jsoncap) for capped streaming JSON and [httpx](https://github.com/cplieger/httpx) for the redirect policy, needs Go 1.27.2 or later and is licensed under Apache-2.0.

## Why use it

mapread is built for tools that turn an AniList, AniDB, TVDB or TMDB id into the others, such as a Sonarr or Radarr helper, and must not guess.

- It applies the file's reading rule, so a lookup tells "lands nowhere" from "nothing stated" and from "no row", and flags a placement that rests only on a default.
- Every TVDB placement a lookup answers with (`Episode`, `OnTVDB`, `AtTVDB`, `Series`, `Routes`) names its series, so a special TVDB files under another show is found on that show.
- It gives the TVDB episode id of a placement where the file holds it, and finds the AniDB episodes on a TVDB episode by that id, so a reader whose series is numbered in another order than TVDB's official one can still place an episode.
- It tells an episode AniDB lists, with its AniDB episode id, from a number AniDB does not list, and both from an anime whose list the file does not hold.
- It shows ambiguity instead of hiding it: a TVDB episode or a TMDB movie returns every AniDB episode on it.
- It refuses a file of another version or of the earlier version 2 shape, or one that breaks a rule listed under [Decode rules](#decode-rules), with errors a caller can match, so a caller keeps its last good copy.
- It downloads a release at one tag and checks the file against its `.sha256` sidecar, with redirects allowed only to GitHub's hosts over HTTPS.
- Decoding streams the file under caps the caller sets, and every lookup returns a copy.

Read the file with `jq` instead if you need a one-off answer from a shell.

## Install

```sh
go get github.com/cplieger/animap/mapread@latest
```

## Usage

```go
src := mapread.Source{Repo: "cplieger/animap"}
client := &http.Client{Timeout: time.Minute}

rel, err := src.Latest(ctx, client)
if err != nil {
	return err
}
if rel.Tag == heldTag {
	return nil // the copy you hold is the latest
}
got, err := rel.Fetch(ctx, client, mapread.DefaultLimits())
if err != nil {
	return err // keep the copy you hold
}
x := mapread.NewIndex(got.Document)

e, ok := x.Entry(21) // AniList 21
if ok {
	for _, s := range x.TVDBSeasons(e) {
		fmt.Println(s.Series, s.Seasons)
	}
}
```

`Latest` reads only the redirect GitHub answers for the latest release, so comparing its tag with the one you hold costs one request. The package examples show `TVDBSeasons`, `AtTVDB`, `AtTVDBEpisodeID` and `Episode` over a small file, and `go test` keeps them true.

## How a lookup reads an episode

An anime row lists runs of AniDB episodes of one kind. A run's `tvdb` lists the TVDB episodes its first episode lands on, each on the row's `tvdb_id` unless the target names its own `series`, and each later episode of the run lands one episode on. A target's `episode_ids`, when present, are the TVDB episode ids of those episodes, one per episode of the run; a target without them has none the file holds. A run with `"tvdb": []` lands nowhere on TVDB, and a run with no `tvdb` member states nothing there. A run's `tmdb_movies` are the TMDB movies each of its episodes is, several ids being aliases of one work; a run without it states no movie.

An episode no run names lands nowhere on TVDB and is no movie when its number is from 1 to the kind's `last`, or when the row holds AniDB's list (`episodes_listed`); past `last` on a row without the list, the file does not place it. An anime with no `tvdb_id` is on TVDB only where a run lands, on the series its targets name. `Episode` applies this rule and gives TVDB a `Statement` in one of three states, `Lands` with its placements, `Nowhere` or `Unstated`, or none when the episode is off TVDB; only `Lands` carries placements. A `Placement` is not a row's `Target`: it names its series, never 0, and carries its TVDB episode id, 0 when the file holds none, and its default flag. Every other lookup applies the same rule.

`Episode` also says what AniDB's list holds. `Listed` is an episode AniDB lists, with its AniDB episode id in `ID`. `NotListed` is a number AniDB does not list on an anime whose list the file holds, and `ListingUnknown` an anime whose list the file does not hold. It returns false for a reference that names no episode (an unknown kind or a number below 1), for an anime with no row, or for a number outside the anime's runs and past its `last` when the file holds no list for it. `Episodes`, `AtTVDB`, `AtTVDBEpisodeID` and `AtTMDBMovie` give each AniDB episode's id beside it, 0 where AniDB does not list it.

`Routes` gives the ids an entry reaches. Its TVDB series are, per part, the series its episodes land on, in landing order, or the anime's `tvdb_id` when none of them lands. Its TMDB movies are the one list every episode of the entry states, the work the entry wholly is, and none when an episode states another list or none: an entry whose episodes are different works gets no movie, never their union. Its IMDb ids are those of each anime a part names whole. `Movies` lists the distinct works of an entry's episodes, and `Anime.TVDBSeries` every series an anime's episodes may land on, its `tvdb_id` first.

## Decode rules

`Decode` refuses a document, with `ErrInvalid` unless another error is named, when:

- its first member is not `version` 2 (`ErrVersion`, carrying a `*VersionError` with the version found), its `sources` names a member of the earlier version 2 shape, `anime_offline_database`, `anime_lists`, `anidb_mirror` or `overlay` (`ErrVersion`, with no `*VersionError`), or its members are not `version`, `generated_at`, `sources`, `attribution`, `anime` and `entries` in that order, each once (`ErrNotDocument`); a member it does not know is skipped at every level, and a key repeated in one object, case-insensitively, is refused;
- it is not UTF-8 (`ErrNotDocument`), a known member is null or of another JSON type, an integer is not written with digits alone, or an episode number or count is below 1 or a season below 0;
- `generated_at` is not an RFC 3339 time in UTC, `sources.mapping.revision` is empty or over 100 bytes, `sources.anidb.titles_dump` is not a calendar date written `yyyy-mm-dd`, or the attribution's licences are not `ODbL-1.0` and `DbCL-1.0`;
- the anime are not ascending by `anidb_id`, a row has no `anidb_id` or one below 1 or carries nothing besides its id, `type` is not one of `TV`, `MOVIE`, `OVA`, `ONA`, `SPECIAL` and `UNKNOWN`, `tvdb_id` is below 1, `mal_ids` is empty, not ascending, repeats an id or holds an id below 1, an IMDb id is malformed or repeated, `last` or `episodes` is empty, or an episode number, count or season is above 1,048,576;
- the runs are not ascending by kind (regular, special, credit, promotion, parody, other) and start, overlap, or two consecutive runs continue each other: the next number, AniDB listing both or neither, targets that keep their series, season and default flag one episode on and hold `episode_ids` on both or neither, and equal `tmdb_movies`, which must be written as one run;
- a run lacks `kind`, `start` or `end`, starts after its end, lists AniDB episode ids on a row without `episodes_listed`, not one per episode or below 1, ends past its kind's `last` (only a regular run of listed episodes stating neither `tvdb` nor `tmdb_movies` may), or lists no AniDB episode and states no landing: its `tvdb` is `[]`, or absent on a row with no `tvdb_id`, and it has no `tmdb_movies`;
- a run's `tmdb_movies` is empty, not ascending, repeats an id or holds an id below 1;
- a target lacks `season` or `episode`; a run's targets are not ascending by series (the target's `series`, else the row's `tvdb_id`), season and episode, each once; a target's `series` is below 1 or is the row's `tvdb_id`; on a row with no `tvdb_id`, `tvdb` is `[]` or a target has no `series`; `default` is present and not true; a target's last landing is above 1,048,576; or a target's `episode_ids` are not one per episode of its run or hold an id below 1;
- one TVDB episode (series, season and episode) has two TVDB episode ids, or one id names two TVDB episodes, in any rows;
- an entry has no `anilist_id` or one below 1, the entries are not ascending by it, `ids` is empty, or `ids.anidb` or `ids.mal` is empty, not ascending and unique, or holds an id below 1;
- a part lacks `anidb_id` or `kind`, names an anime with no row, has one of `start` and `end`, starts after its end, ends past its anime's `last`, or is `combined` but not regular with no range; or `parts` is empty;
- it is over a cap (`ErrTooLarge`).

It does not check an entry's parts against its `ids.anidb`.

## API

- `Decode` reads a document under `Limits`: a zero field takes its `DefaultLimits()` value: 32 MiB, 131,072 rows per table, 256 KiB per row, and 2,097,152 AniDB episodes named by the entries' parts and as many named by the runs' `tmdb_movies`, counted once per movie. It refuses a document that is not UTF-8 with `ErrNotDocument`. `Encode` writes the canonical form. `ContentHash` is the SHA-256 of the version, attribution, anime and entries, so `generated_at` and `sources` never change it.
- `Source.Latest` and `Release.Fetch` download and verify a release. `Fetched.Bytes` is the document's size as downloaded.
- `NewIndex` and its lookups: `Entry`, `Anime`, `Episodes`, `Episode`, `Routes`, `Movies`, `OnTVDB`, `TVDBSeasons`, `AtTVDB`, `AtTVDBEpisodeID`, `AtTMDBMovie` and `Series`. `OnTVDB` reads one entry episode on TVDB, on each series it lands on and on its anime's own series, where an episode filed only elsewhere lands on no season; its `On` answers for any series, and an entry episode one of whose AniDB episodes has an `Unstated` statement reads as unstated on every series it does not land on, never as fully stated there, while one off TVDB reads as landing nowhere there. `TVDBSeasons` groups those readings on every series one of the entry's episodes names, counting every entry episode on each: an entry episode rests on a default on a season when any of its AniDB episodes lands there by default. `AtTVDBEpisodeID` finds every AniDB episode on the TVDB episode an id names, as `AtTVDB` does there. `Part.Identity` tells the part that is a whole anime.
- Errors to match with `errors.Is`: `ErrVersion` (with `*VersionError` naming the version found, none for the earlier version 2 shape), `ErrNotDocument`, `ErrInvalid`, `ErrTooLarge`, `ErrChecksum`, `ErrRelease` and `ErrRedirect`. An empty, truncated or failed read matches none of them, and nor does an HTTP 408, 425, 429 or 5xx answer, so a caller can retry it. Every answer `Latest` or `Fetch` refuses for its HTTP status or its redirect carries a `*StatusError` with its URL and status code, for `errors.As`; a 200 answer refused for its content (`ErrChecksum`, `ErrTooLarge` or a decode error) carries none.

Decoding holds the whole document and its index in memory: set `Limits.MaxDocumentBytes` to what your memory allows.

The full reference is on [pkg.go.dev](https://pkg.go.dev/github.com/cplieger/animap/mapread).

## Contributing

See [CONTRIBUTING.md](../CONTRIBUTING.md).

## Disclaimer

This project is built with care and follows security best practices, but it is intended for personal / self-hosted use. No guarantees of fitness for production environments. Use at your own risk.

This project was built with AI-assisted tooling using [Claude](https://claude.com), [GPT](https://openai.com), and [Kiro](https://kiro.dev). The human maintainer defines architecture, supervises implementation, and makes all final decisions.

## License

Apache-2.0. See [LICENSE](LICENSE).
