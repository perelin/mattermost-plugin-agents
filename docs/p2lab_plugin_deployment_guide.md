# Mattermost Plugin Deployment Guide — P2Lab Server

Generic guide for deploying any standard Mattermost plugin to the P2Lab Mattermost instance.

## Target Server

- **URL**: `https://mattermost.p2lab.com`
- **Auth**: Admin personal access token

## Prerequisites

- **Go** (1.23+) on PATH
- **Node.js / npm** on PATH
- **git** on PATH
- You are in the root directory of a standard Mattermost plugin repository (one that uses the official plugin build scaffolding with `build/setup.mk` and `build/pluginctl`)

## Step 1: Configure Credentials

Create a `.env` file at the repo root:

```
MM_SERVICESETTINGS_SITEURL=https://mattermost.p2lab.com
MM_ADMIN_TOKEN=<admin personal access token>
```

Most standard Mattermost plugin Makefiles already include `-include .env` and export these variables. If the Makefile does **not** have this, add these two lines near the top:

```makefile
-include .env
export MM_SERVICESETTINGS_SITEURL
export MM_ADMIN_TOKEN
```

Alternatively, export them manually before running make:

```bash
export MM_SERVICESETTINGS_SITEURL=https://mattermost.p2lab.com
export MM_ADMIN_TOKEN=<admin personal access token>
```

## Step 2: Build & Deploy

```bash
make deploy
```

This is the standard command across all Mattermost plugins using the official build tooling. It does:

1. Propagates `plugin.json` manifest into source directories
2. Runs `go generate`
3. Builds Go server binaries (multi-arch: linux, darwin, windows)
4. Installs npm dependencies and builds the webapp (if the plugin has one)
5. Bundles everything into `dist/<plugin-id>-<version>.tar.gz`
6. Uploads the bundle to the server via `UploadPluginForced` API
7. Enables the plugin via `EnablePlugin` API

## Step 3: Verify

```bash
make logs
```

Or to tail continuously:

```bash
make logs-watch
```

The plugin should appear as enabled in **System Console -> Plugins -> Plugin Management**.

## Useful Commands

| Command | What it does |
|---------|-------------|
| `make deploy` | Full build + upload + enable |
| `make dist` | Build + bundle only (no upload) |
| `make reset` | Disable + re-enable (restart plugin) |
| `make disable` | Disable the plugin |
| `make enable` | Enable the plugin |
| `make logs` | Fetch plugin logs from server |
| `make logs-watch` | Tail plugin logs |

## How Authentication Works

The `pluginctl` tool (built automatically from `build/pluginctl/`) tries to connect in this order:

1. **Unix socket** (local mode) — only works if Mattermost runs on the same machine
2. **`MM_ADMIN_TOKEN`** — personal access token with admin privileges (this is what P2Lab uses)
3. **`MM_ADMIN_USERNAME` + `MM_ADMIN_PASSWORD`** — username/password fallback

## Troubleshooting

| Error | Cause / Fix |
|-------|-------------|
| `MM_SERVICESETTINGS_SITEURL is not set` | `.env` missing or Makefile doesn't source it — export vars manually |
| `failed to upload plugin bundle` | Plugin uploads disabled in System Console, or token lacks admin rights |
| `failed to enable plugin` | Plugin crashed on startup — check `make logs` for details |
| Build fails on `npm install` | Delete `webapp/node_modules` and retry |
| Build fails on Go | Check Go version matches plugin's `go.mod` |

## Notes

- The plugin ID and version are read from `plugin.json` at the repo root
- Min Mattermost server version is also specified in `plugin.json`
- If you only need a Linux build (e.g. CI), use `make dist-ci` instead of `make dist`
- The `.env` file should be gitignored — never commit credentials
- The admin token can be created in **System Console -> Integrations -> Bot Accounts** or via the user's **Security -> Personal Access Tokens** settings
