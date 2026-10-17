# mapread

mapread gives a Go program the anime id mapping in animap's `animap.json`: which AniDB episodes an AniList entry is, which episodes AniDB lists with their AniDB episode ids, and where each lands on TheTVDB and TMDB.

It reads, checks and downloads the version 2 file, and answers lookups over it in memory. It depends on two small libraries by the same author, [jsoncap](https://github.com/cplieger/jsoncap) for capped streaming JSON and [httpx](https://github.com/cplieger/httpx) for the redirect policy, needs Go 1.27.2 or later and is licensed under Apache-2.0.

## Why use it

mapread is built for tools that turn an AniList, AniDB, TVDB or TMDB id into the others, such as a Sonarr or Radarr helper, and must not guess.

- It applies the file's reading rule, so a lookup tells "lands nowhere" from "nothing stated" and from "no row", and flags a placement that rests only on a default.
- It tells an episode AniDB lists, with its AniDB episode id, from a number AniDB does not list, and both from an anime whose list the file does not hold.
- It shows ambiguity instead of hiding it: a TVDB episode returns every AniDB episode on it.
- It refuses a file of another version, or one that breaks a rule it checks, with errors a caller can match, so a caller keeps its last good copy. It checks the types, the caps and the rules `docs/schema.md` lists under "Order and checks", except the order of entries with no AniList id, whose sort key can hold services a newer file adds. It does not check an entry's parts against its `ids.anidb`.
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

`Latest` reads only the redirect GitHub answers for the latest release, so comparing its tag with the one you hold costs one request. The package examples show `TVDBSeasons`, `AtTVDB` and `Episode` over a small file, and `go test` keeps them true.

## How a lookup reads an episode

An anime row lists runs of episodes and where each run lands on each site. A run with `[]` for a site lands nowhere there, and a run with no member for a site states nothing. An episode no run names lands nowhere on every site the anime has an id for when its number is from 1 to the kind's `last`, or when the row holds AniDB's list (`episodes_listed`); past `last` on a row without the list, the file does not place it. `Episode` applies this rule and gives each site a `Statement` in one of three states: `Lands` with its targets, `Nowhere`, or `Unstated`. Only `Lands` carries targets. Every other lookup applies the same rule.

`Episode` also says what AniDB's list holds. `Listed` is an episode AniDB lists, with its AniDB episode id in `ID`. `NotListed` is a number AniDB does not list on an anime whose list the file holds, and `ListingUnknown` an anime whose list the file does not hold. It returns false for a reference that names no episode (an unknown kind or a number below 1), for an anime with no row, or for a number outside the anime's runs and past its `last` when the file holds no list for it. `Episodes` and `AtTVDB` give each AniDB episode's id beside it, 0 where AniDB does not list it. The full format is in [docs/schema.md](../docs/schema.md).

## API

- `Decode` reads a document under `Limits`: a zero field takes its `DefaultLimits()` value: 32 MiB, 131,072 rows per table, 256 KiB per row and 2,097,152 episodes named by the entries. It refuses a document that is not UTF-8 with `ErrNotDocument`. `Encode` writes the canonical form.
- `Source.Latest` and `Release.Fetch` download and verify a release. `Fetched.Bytes` is the document's size as downloaded.
- `NewIndex` and its lookups: `Entry`, `Anime`, `Episodes`, `Episode`, `Routes`, `OnTVDB`, `TVDBSeasons`, `AtTVDB`, `AtTMDB`, `TMDBMovie` and `Series`. `OnTVDB` reads one entry episode on TVDB, and `TVDBSeasons` groups those readings: an entry episode rests on a default on a season when any of its AniDB episodes lands there by default. `Part.Identity` tells the part that is a whole anime, the one an entry anime-offline-database or an identity link gives a single AniDB id has.
- Errors to match with `errors.Is`: `ErrVersion` (with `*VersionError` naming the version found), `ErrNotDocument`, `ErrInvalid`, `ErrTooLarge`, `ErrChecksum`, `ErrRelease` and `ErrRedirect`. An empty, truncated or failed read matches none of them, and nor does an HTTP 408, 425, 429 or 5xx answer, so a caller can retry it. Every answer `Latest` or `Fetch` refuses for its HTTP status or its redirect carries a `*StatusError` with its URL and status code, for `errors.As`; a 200 answer refused for its content (`ErrChecksum`, `ErrTooLarge` or a decode error) carries none.

A decoded file takes about 2.1 bytes of memory per byte of file, and 2.2 with its index: about 30 MB for today's 13.4 MB file. Set `Limits.MaxDocumentBytes` to what your memory allows.

The full reference is on [pkg.go.dev](https://pkg.go.dev/github.com/cplieger/animap/mapread).

## Contributing

See [CONTRIBUTING.md](../CONTRIBUTING.md).

## Disclaimer

This project is built with care and follows security best practices, but it is intended for personal / self-hosted use. No guarantees of fitness for production environments. Use at your own risk.

This project was built with AI-assisted tooling using [Claude](https://claude.com), [GPT](https://openai.com), and [Kiro](https://kiro.dev). The human maintainer defines architecture, supervises implementation, and makes all final decisions.

## License

Apache-2.0. See [LICENSE](LICENSE).
