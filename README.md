# Poople

A private, server-rendered fork of [Poople](https://pooplegame.com/), the daily word ladder: change the day's start word into POOP one letter at a time. It uses every four-letter word in Collins Scrabble Words 2024, follows Poople's daily calendar (new puzzle at 08:00 UTC), and adds optional accounts with profile pictures, leaderboards, statistics and an unlimited mode.

## Run it

Requires Go 1.26 and [Task](https://taskfile.dev).

```sh
task setup:dev               # once: install pinned code generators into .tools/bin
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

## Deploy

Deploy from Linux x86_64 to a Linux x86_64 host running systemd behind Caddy. Create private copies of the examples (all gitignored) and edit them:

```sh
cp .deploy.env.example .deploy.env               # DEPLOY_HOST (local or SSH alias), DEPLOY_DIR, DEPLOY_URL (HTTPS)
cp deploy/poople.service.example deploy/poople.service
cp deploy/Caddyfile.example deploy/Caddyfile
```

In the unit, replace `{{USER}}` and `{{GROUP}}` and make every `/srv/poople` path match `DEPLOY_DIR`; set the Caddy domain to match `DEPLOY_URL`. The target needs rsync and passwordless `sudo -n` for `systemctl`. Once, on the host as the deployment user:

```sh
sudo install -d -m 0700 -o "$USER" -g "$(id -gn)" /srv/poople
sudo install -m 0644 deploy/poople.service /etc/systemd/system/poople.service
sudo systemctl daemon-reload
```

Add the `deploy/Caddyfile` block to the host's Caddyfile (`flush_interval -1` keeps SSE streaming), then validate and reload Caddy. Leave the service stopped; then run:

```sh
task deploy
```

It checks the host, runs `task check` and `task race`, stops `poople.service`, backs up `data/` to `<DEPLOY_DIR>/backups/<UTC time>`, copies only `bin/poople`, starts the service and expects HTTP 200 from `DEPLOY_URL`. A failed backup or copy leaves the service stopped; fix it and rerun. Backups are never pruned.

On first start the admin setup link is in the service log (`sudo journalctl -u poople`); open its `/setup?token=…` path on `DEPLOY_URL`.
