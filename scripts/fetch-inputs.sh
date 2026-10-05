#!/usr/bin/env bash
# Download the upstreams by content address into CACHE_DIR, verifying each
# against its address. A file already in the cache is re-verified, not
# re-downloaded. Prints the paths as AOD_PATH= and LIST_PATH= lines. With
# AOD_VERSION empty, only the Anime-Lists file is fetched.
set -euo pipefail

AOD_VERSION="${AOD_VERSION:-}"
LIST_COMMIT="${LIST_COMMIT:?}"
LIST_BLOB="${LIST_BLOB:?}"
CACHE_DIR="${CACHE_DIR:?}"
CURL=(curl --proto '=https' --proto-redir '=https' --tlsv1.2 --connect-timeout 20 --max-time 600
  --retry 3 --retry-delay 5 -fsSL)

if [ -n "$AOD_VERSION" ]; then
  AOD_SHA256="${AOD_SHA256:?}"
  aod="${CACHE_DIR}/aod/${AOD_SHA256}.jsonl"
  mkdir -p "${CACHE_DIR}/aod"
  if ! [ -f "$aod" ] || [ "$(sha256sum "$aod" | cut -c1-64)" != "$AOD_SHA256" ]; then
    # The animap build refuses a database over 256 MiB, so a download over
    # it can never be used.
    "${CURL[@]}" --max-filesize 268435456 -o "${aod}.part" \
      "https://github.com/cedya77/anime-offline-database/releases/download/${AOD_VERSION}/anime-offline-database.jsonl"
    got=$(sha256sum "${aod}.part" | cut -c1-64)
    if [ "$got" != "$AOD_SHA256" ]; then
      rm -f "${aod}.part"
      echo "fetch-inputs: ERROR offline database sha256 ${got}, release says ${AOD_SHA256}" >&2
      exit 1
    fi
    mv "${aod}.part" "$aod"
  fi
  printf 'AOD_PATH=%s\n' "$aod"
fi

list="${CACHE_DIR}/list/${LIST_COMMIT}.xml"
mkdir -p "${CACHE_DIR}/list"
if ! [ -f "$list" ] || [ "$(git hash-object "$list")" != "$LIST_BLOB" ]; then
  "${CURL[@]}" --max-filesize 33554432 -o "${list}.part" \
    "https://raw.githubusercontent.com/Anime-Lists/anime-lists/${LIST_COMMIT}/anime-list-master.xml"
  got=$(git hash-object "${list}.part")
  if [ "$got" != "$LIST_BLOB" ]; then
    rm -f "${list}.part"
    echo "fetch-inputs: ERROR anime-list-master.xml blob ${got}, commit ${LIST_COMMIT} says ${LIST_BLOB}" >&2
    exit 1
  fi
  mv "${list}.part" "$list"
fi
printf 'LIST_PATH=%s\n' "$list"
