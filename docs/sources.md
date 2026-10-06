# Data sources and licences

`animap.json` is built from two open sources, episode counts from a mirror of AniDB's data, and animap's own overlay. This page names each source, what animap takes from it, and the licence that applies.

## The published file

`animap.json` is made available under the [Open Database License (ODbL) 1.0](https://opendatacommons.org/licenses/odbl/1-0/), and its contents under the [Database Contents License (DbCL) 1.0](https://opendatacommons.org/licenses/dbcl/1-0/). Both texts are in [LICENSE-DATA](../LICENSE-DATA). The file carries the same notice in its `attribution` member, and every release repeats it in its notes. If you publish a database built from `animap.json`, the ODbL asks you to keep that notice and to share your database under the same licence.

## anime-offline-database

[anime-offline-database](https://github.com/cedya77/anime-offline-database) supplies the AniList, AniDB and MyAnimeList ids and the type, and the episode count of an anime with no end date in the AniDB mirror. It is made available under the ODbL 1.0 and the DbCL 1.0. That share-alike licence is why `animap.json` is published under the ODbL too.

## Anime-Lists

[Anime-Lists](https://github.com/Anime-Lists/anime-lists) supplies the TVDB, TMDB and IMDb ids, the seasons and offsets, and the mapping list. Anime-Lists publishes no licence. animap redistributes these fields and credits the project here, in `NOTICE` and in the file's `attribution` member.

## The AniDB mirror

[AnimeAggregations](https://github.com/notseteve/AnimeAggregations) republishes AniDB's anime data as one JSON file per anime, in a new snapshot on the 1st and 15th of each month. Each build reads the snapshot pinned in `.github/workflows/publish.yaml`, and `sources.anidb_mirror` in `animap.json` names that commit. The build keeps the episode numbers, the air dates and whether AniDB records each anime's end date. The archive, with its titles and descriptions, goes straight from the download into the build and is never written to disk, the repository or a release.

`animap.json` publishes one fact from it: the `episodes` count of a record whose anime has an end date in the mirror. The collision check and the special-of-parent bridges also read the mirror's specials and air dates, and publish none of them. A count is a fact about AniDB's episode list, not a creative work, and it is the only part of the mirror animap publishes. The repository is labelled with the Unlicense, but its content is AniDB's data, which AniDB licenses under CC BY-NC-SA 4.0, so animap credits AniDB as the source of the counts in `NOTICE`, in the README and in the file's `attribution` member, and uses them for nothing commercial.

## TheTVDB

The overlay's TVDB fingerprints hold episode numbers and air dates from TheTVDB's official order, read through Sonarr's public metadata service. `animap.json` holds no TVDB content beyond the ids and season numbers Anime-Lists already carries.

## AniDB

AniDB states that its data is licensed under [CC BY-NC-SA 4.0](https://creativecommons.org/licenses/by-nc-sa/4.0/). animap publishes no AniDB titles, descriptions or other text in `animap.json`. The file holds only mapping facts, such as an AniDB id, an episode count and number, and the TVDB or TMDB episode it corresponds to. The AniDB ids come from anime-offline-database.

A maintainer reads AniDB's episode list when writing an overlay entry, a special-of-parent bridge or a `checks/counts.json` row, and links the AniDB page. The repository keeps only the count in a `checks/counts.json` row, and copies nothing else from that list. No workflow reads AniDB itself. The publish workflow reads AniDB's episode lists from the AniDB mirror above. AniDB titles and descriptions are never written to the repository or a release.
