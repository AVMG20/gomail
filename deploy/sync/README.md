# gomail-sync on Coolify

`gomail-sync` keeps gomail's settings, mailboxes, labels, rules and (if you
want) sign-ins the same on all your computers. This guide puts it on
[Coolify](https://coolify.io) at `https://gomail.avmg.nl`, the server gomail
suggests by default.

The server can't read what it stores. gomail encrypts everything on the
computer with a key from the **sync code**: 24 words gomail makes when you
turn on sync. The server only keeps an account id and a hash of an auth key,
both derived from the code. Without the code nobody, including you, can read
the synced data, so keep the code somewhere safe.

## What you need

- A Coolify server (v4) with its proxy (Traefik or Caddy) running.
- DNS for `gomail.avmg.nl` pointing at that server (an `A`/`AAAA` record).
- This repository reachable by Coolify (public GitHub, or a GitHub App /
  deploy key for a private fork).

## Set it up

1. **DNS.** Create an `A` record `gomail.avmg.nl` → your Coolify server's IP
   (and `AAAA` for IPv6). Wait until `dig gomail.avmg.nl` shows it.
2. **New resource.** In Coolify: *Projects* → your project → *+ New* →
   *Public Repository* (or *Private Repository (with GitHub App)*), and enter
   the repository URL, e.g. `https://github.com/AVMG20/gomail`. Branch: `main`.
3. **Build pack.** Choose **Docker Compose**. Set
   - *Base Directory*: `/`
   - *Docker Compose Location*: `/deploy/sync/docker-compose.yml`

   and continue. Coolify reads the compose file and shows one service,
   `gomail-sync`.
4. **Domain.** Open the `gomail-sync` service settings and set *Domains* to
   `https://gomail.avmg.nl:8080`. The `:8080` tells Coolify's proxy which
   container port to send traffic to; visitors use plain
   `https://gomail.avmg.nl`. Coolify requests the Let's Encrypt certificate.
   Don't add a port mapping: the container isn't published on the host.
5. **Environment variables** (*Environment Variables* tab, all optional):

   | Variable     | Default | Meaning |
   |--------------|---------|---------|
   | `SIGNUPS`    | `open`  | `open`: anyone who reaches the server can turn on sync (make a new sync code). `closed`: only codes already in use keep working. |
   | `QUOTA_MB`   | `64`    | Storage per sync account. |
   | `MAX_DOC_MB` | `16`    | Largest single document. |
   | `MAX_ACCOUNTS` | `1000` | Most sync accounts the server takes (`0`: no limit). |
   | `TRUST_PROXY` | `1`    | Count rate limits per client address from `X-Forwarded-For`. It is only believed from a private or local address (Coolify's proxy). |
   | `LOG_LEVEL`  | `info`  | `debug`, `info`, `warn` or `error`. |

6. **Deploy.** Click *Deploy*. The first build takes a minute or two. The
   service turns *Healthy* once `/healthz` answers.
7. **Check it.** `curl https://gomail.avmg.nl/healthz` prints `{"ok":true}`.

## Connect your computers

1. On the first computer: gomail → *Settings → Sync* → **Turn on sync**.
   gomail shows the 24-word sync code. Copy it and keep it somewhere safe
   (a password manager is a good place).
2. On every other computer: *Settings → Sync* → **Use a code from another
   device**, paste or type the 24 words, then **Connect**. That computer takes
   the synced settings; mailboxes and labels only it had are added.

If you use your own address instead of `gomail.avmg.nl`, open *Server* on
that page first and enter it, on every computer.

### Lock it down

With `SIGNUPS=open`, anyone who finds the server can create a sync account
and use up to `QUOTA_MB` of disk (at most `MAX_ACCOUNTS` of them). Once all
your computers are connected, set
`SIGNUPS=closed` and redeploy: your code keeps working, new codes are
refused. Turn it back to `open` when you need a new sync account (you don't
for a new computer: it uses the existing code).

## Data and backups

Everything lives in the `gomail-sync-data` volume (`/data/sync.db`, SQLite).
In Coolify, add a *Scheduled Backup* for the volume, or back it up from the
host (`docker volume inspect` shows its path). Restoring a backup is safe:
computers merge what changed since and upload it.

Losing the volume costs no settings, as long as a computer still syncs:
each one keeps its own data. With `SIGNUPS=open`, the first computer that
syncs signs up again under your sync code and uploads everything; the
others follow. With `SIGNUPS=closed`, they show that the server lost your
data: set `SIGNUPS=open`, redeploy, click *Sync now* on one computer, then
close sign-ups again. Use a computer that already synced for this: a new
one can only join once the account is back.

## Updating

Click *Redeploy* in Coolify (or turn on automatic deployment on push). The
release workflow in `.github/workflows` only builds the desktop app; build
the server image by hand if you want one outside Coolify:

```sh
docker build -f deploy/sync/Dockerfile -t gomail-sync .
docker run -d --name gomail-sync -p 127.0.0.1:8080:8080 -v gomail-sync-data:/data gomail-sync
```

Put any HTTPS reverse proxy in front of it (gomail only connects over
`https://`, except to `localhost`). Set `TRUST_PROXY=1` when the proxy sets
`X-Forwarded-For` and reaches the server from a private or local address,
so rate limits count per client.

## Limits and behavior

- Sign-ups and logins are rate limited per client address (30 per 10
  minutes; IPv6 per /64), and logins per account and address (10 per 15
  minutes).
- Uploads and downloads of a document may take up to 15 minutes, other
  requests a minute. On shutdown the server finishes the requests it is
  handling.
- Documents are replaced as a whole, with a revision check: a computer that
  uploads a stale copy is refused, merges the newer one and tries again, so
  changes from two computers are both kept (per setting, the last change
  wins).
- Without the sync code the data can't be decrypted or recovered. If you
  lose it but still have a computer that syncs, *Settings → Sync → Show sync
  code* shows it again.
