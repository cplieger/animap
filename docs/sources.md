# Data sources and licences

`animap.json` is built from two open sources, AniDB's episode lists from a mirror of AniDB's data, and animap's own overlay. This page names each source, what animap takes from it, and the licence that applies.

## The published file

`animap.json` is made available under the [Open Database License (ODbL) 1.0](https://opendatacommons.org/licenses/odbl/1-0/), and its contents under the [Database Contents License (DbCL) 1.0](https://opendatacommons.org/licenses/dbcl/1-0/). Both texts are in [LICENSE-DATA](../LICENSE-DATA). The file carries the same notice in its `attribution` member, and every release repeats it in its notes. If you publish a database built from `animap.json`, the ODbL asks you to keep that notice and to share your database under the same licence.

## anime-offline-database

[anime-offline-database](https://github.com/cedya77/anime-offline-database) supplies every entry's ids on the anime sites it lists (AniList, AniDB, MyAnimeList, Kitsu, Anime-Planet, aniSearch, Anime News Network, LiveChart, Simkl, AnimeCountdown and notify.moe), its type and its episode count, and the regular episode count of an anime with no end date in the AniDB mirror. An Anime-Planet or notify.moe id is that site's own URL key, which for Anime-Planet is derived from a title. animap publishes it because it is the only key that service has. It is made available under the ODbL 1.0 and the DbCL 1.0. That share-alike licence is why `animap.json` is published under the ODbL too.

## Anime-Lists

[Anime-Lists](https://github.com/Anime-Lists/anime-lists) supplies the TVDB, TMDB and IMDb ids, and where each AniDB episode lands on TVDB and TMDB: its seasons, offsets and mapping rows, for every episode kind its rows name. Anime-Lists publishes no licence. animap redistributes these facts and credits the project here, in `NOTICE` and in the file's `attribution` member.

## The AniDB mirror

[AnimeAggregations](https://github.com/notseteve/AnimeAggregations) republishes AniDB's anime data as one JSON file per anime, in a new snapshot on the 1st and 15th of each month. Each build reads the snapshot pinned in `.github/workflows/publish.yaml`, and `sources.anidb_mirror` in `animap.json` names that commit. The build keeps each episode's kind, number and AniDB episode id, the air dates of the regular episodes and specials, whether AniDB records each anime's end date, and SHA-256 digests of the episode titles, which the date check's acceptance records and the overlay links compare. The archive, with its titles and descriptions, goes straight from the download into the build and is never written to disk, the repository or a release.

The repository is labelled with the Unlicense, but its content is AniDB's data, which AniDB licenses under CC BY-NC-SA 4.0. From it, `animap.json` carries only which episodes exist, their kind and number, and their AniDB ids: the `episodes_listed` and `anidb_episode_ids` members, and the highest number of each kind in `last`. It carries none of the content AniDB holds for an episode: no titles, summaries, air dates, lengths, ratings or any other description, and no AniDB links to other sites. `NOTICE`, the README and the file's `attribution` member credit AniDB as the source and AnimeAggregations as the copy read. The air dates and title digests stay inside the build, where the date check and the overlay links read them. A link holds a SHA-256 digest of the titles of the AniDB episodes it names, and never a title. A mirror file animap refuses, for example one listing an episode twice, gives its anime no AniDB episode list.

The [date check](date-check.md) compares the mirror's air dates with TVDB's and TMDB's. Its issues show the episode numbers and air dates of the episodes that fail, facts of the same kind as the published episode numbers, and never a title.

## TheTVDB

The overlay's TVDB fingerprints hold episode numbers and air dates from TheTVDB's official order, read through Sonarr's public metadata service. The [daily review](review.md) reads the same service, keeps only episode numbers, absolute numbers, air dates, the series URL slug and the first 16 hex characters of a SHA-256 of each episode's title between runs, and its issues show only episode numbers and air dates. `animap.json` holds no TVDB content beyond the ids and season and episode numbers Anime-Lists carries, and the season and episode numbers of absolute-numbered episodes, read from an overlay entry's captured TVDB order. It holds no TVDB titles or dates.

## TMDB

This product uses the TMDB API but is not endorsed or certified by TMDB.

The daily review's [date check](date-check.md) reads TMDB's episode order for every show Anime-Lists maps, and whether each TMDB movie id still exists, through the [TMDB API](https://developer.themoviedb.org/), with a read access token from a repository secret. Between runs it keeps only season and episode numbers, air dates, and the first 16 hex characters of a SHA-256 of each episode's title, and for a movie only whether it exists. Its issues show only episode numbers and air dates, never a TMDB title. `animap.json` holds no TMDB content beyond the ids and season and episode numbers Anime-Lists carries.

## AniDB

AniDB states that its data is licensed under [CC BY-NC-SA 4.0](https://creativecommons.org/licenses/by-nc-sa/4.0/). `animap.json` carries from AniDB only which episodes exist, their kind and number, and their AniDB episode ids, read through the mirror above. It holds no AniDB titles, descriptions or other text. The AniDB anime ids come from anime-offline-database.

A maintainer reads AniDB's episode list when writing an overlay entry, an overlay link or a `checks/counts.json` row, and links the AniDB page. The repository keeps only the count in a `checks/counts.json` row, and copies nothing else from that list. No workflow reads AniDB itself. The publish and daily review workflows read AniDB's episode lists from the AniDB mirror above. AniDB titles and descriptions are never written to the repository or a release.
