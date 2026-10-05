# Third-party notices

animap includes no third-party code beyond its Go module dependencies. One design is followed without any of its code being included:

- The join precedence in `internal/join/join.go` follows [Fribb/anime-lists-generator](https://github.com/Fribb/anime-lists-generator)'s merge. The identity fields come from anime-offline-database, and Anime-Lists fills only the fields that are still empty.

The data sources and their licences are listed in the README's "Data sources and licence" section and in [NOTICE](NOTICE).
