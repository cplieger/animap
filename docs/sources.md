# Data sources and licences

`animap.json` is built from two open sources and animap's own overlay. This page names each source, what animap takes from it, and the licence that applies.

## The published file

`animap.json` is made available under the [Open Database License (ODbL) 1.0](https://opendatacommons.org/licenses/odbl/1-0/), and its contents under the [Database Contents License (DbCL) 1.0](https://opendatacommons.org/licenses/dbcl/1-0/). Both texts are in [LICENSE-DATA](../LICENSE-DATA). The file carries the same notice in its `attribution` member, and every release repeats it in its notes. If you publish a database built from `animap.json`, the ODbL asks you to keep that notice and to share your database under the same licence.

## anime-offline-database

[anime-offline-database](https://github.com/cedya77/anime-offline-database) supplies the AniList, AniDB and MyAnimeList ids, the type and the episode count. It is made available under the ODbL 1.0 and the DbCL 1.0. That share-alike licence is why `animap.json` is published under the ODbL too.

## Anime-Lists

[Anime-Lists](https://github.com/Anime-Lists/anime-lists) supplies the TVDB, TMDB and IMDb ids, the seasons and offsets, and the mapping list. Anime-Lists publishes no licence. animap redistributes these fields and credits the project here, in `NOTICE` and in the file's `attribution` member.

## TheTVDB

The overlay's TVDB fingerprints hold episode numbers and air dates from TheTVDB's official order, read through Sonarr's public metadata service. `animap.json` holds no TVDB content beyond the ids and season numbers Anime-Lists already carries.

## AniDB

AniDB states that its data is licensed under [CC BY-NC-SA 4.0](https://creativecommons.org/licenses/by-nc-sa/4.0/). animap publishes no AniDB titles, descriptions or other text in `animap.json`. The file holds mapping facts: an AniDB id and an episode number, and the TVDB or TMDB episode it corresponds to. The AniDB ids come from anime-offline-database.

AniDB is evidence only. A maintainer reads AniDB's episode list by hand when writing an overlay entry, a special-of-parent bridge or a `checks/counts.json` row, and links the AniDB page. The repository keeps only facts from that list: episode counts, special numbers, and in a bridge the air date of each special. No workflow reads AniDB, and AniDB titles and descriptions are never written to the repository or a release.
