# Poople

A private, server-rendered fork of [Poople](https://pooplegame.com/), the daily word ladder: change the day's start word into POOP one letter at a time. It uses every four-letter word in Collins Scrabble Words 2024, follows Poople's daily calendar (new puzzle at 08:00 UTC), and adds optional accounts with profile pictures, leaderboards, statistics and an unlimited mode.

## Run it

Requires Go 1.26 and [Task](https://taskfile.dev).

```sh
task worktree:setup          # install pinned tools into .tools/bin
task build                   # generate code and build bin/poople
bin/poople --data-dir ./data # serves on 127.0.0.1:8080
```

On first start with no accounts, the server prints a 15-minute admin setup link; open it to create the admin account. Anyone can then play anonymously or sign up.

For development, `task live DATA_DIR=./data` rebuilds and reloads on changes.

Flags: `--listen`, `--secure-cookie` (behind HTTPS), `--trusted-proxy` (take client addresses from `X-Forwarded-For` behind a reverse proxy), `--ui-fps`.

## Checks

```sh
task check   # generate, build, format check, vet and test
task race    # tests with the race detector
```
