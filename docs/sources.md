# Data sources and licences

`animap.json` is built from animap's own mapping database, which the project maintains privately and checks against AniDB's episode lists, TheTVDB's official episode order and TMDB. A new file is built from it each week. This page names each source the file holds information from, what it holds, and the licence that applies.

## The published file

`animap.json` is made available under the [Open Database License (ODbL) 1.0](https://opendatacommons.org/licenses/odbl/1-0/), and its contents under the [Database Contents License (DbCL) 1.0](https://opendatacommons.org/licenses/dbcl/1-0/). Both texts are in [LICENSE-DATA](../LICENSE-DATA). The file carries the same notice in its `attribution` member, and every release repeats it in its notes. If you publish a database built from `animap.json`, the ODbL asks you to keep that notice and to share your database under the same licence.

`sources.mapping.revision` names the state of the mapping database a file was built from, and `sources.anidb.titles_dump` the date of the AniDB anime title list its anime rows reflect.

## AniDB

AniDB states that its data is licensed under [CC BY-NC-SA 4.0](https://creativecommons.org/licenses/by-nc-sa/4.0/). From AniDB, `animap.json` carries which anime and episodes exist, each episode's kind, number and AniDB episode id, the anime types and the MyAnimeList ids AniDB links: the `anidb_id`, `type`, `mal_ids`, `episodes_listed`, `anidb_episode_ids` and `last` members. It carries none of the content AniDB holds for an anime or an episode: no titles, summaries, air dates, lengths, ratings or any other description.

## anime-offline-database

The mapping database started from earlier releases of `animap.json`, which took every entry's ids and type from [anime-offline-database](https://github.com/cedya77/anime-offline-database). The links between AniList, AniDB and MyAnimeList ids it gave are still in the database. anime-offline-database is made available under the ODbL 1.0 and the DbCL 1.0. That share-alike licence is one reason `animap.json` is published under the ODbL too.

## Anime-Lists

The same earlier releases took the TheTVDB, TMDB and IMDb ids, and where each AniDB episode lands on TheTVDB, from [Anime-Lists](https://github.com/Anime-Lists/anime-lists). Those mappings are the database's starting point, and a target flagged `default` still rests only on an Anime-Lists default. Anime-Lists publishes no licence. animap credits the project here, in `NOTICE` and in the file's `attribution` member.

## AniList

The AniList ids are [AniList](https://anilist.co)'s. `animap.json` holds no other AniList content.

## TheTVDB

TVDB data is provided by [TheTVDB](https://thetvdb.com). `animap.json` holds from it the series ids, the season and episode numbers in TheTVDB's official order, and the TheTVDB episode ids. It holds no TheTVDB titles, dates or descriptions.

## TMDB

This product uses the TMDB API but is not endorsed or certified by TMDB.

`animap.json` holds the TMDB movie id of each film and special TMDB lists as a movie, and no other TMDB content: no titles, dates or descriptions.
