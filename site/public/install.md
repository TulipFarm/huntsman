# Install Huntsman for your agents

You are an AI coding agent. Follow these steps to install Huntsman on this machine and connect it to every AI tool
the user has. Huntsman reads web pages and returns clean Markdown: no ads, menus, cookie banners, or tracking
scripts. That costs far fewer tokens than raw HTML. It runs locally, on the user's machine, and needs no account or
API key.

Work through the steps in order. Run each command yourself. Ask the user only where a step says to.

## 1. Check the system

Run `uname -s` and `uname -m`.

- **macOS or Linux**, on arm64/aarch64 or x86_64/amd64: continue to step 2.
- **Windows** (PowerShell, cmd, or a `MINGW`/`MSYS` shell): the native install doesn't support Windows. If `wsl`
  works, run every command below inside WSL. Otherwise, if `docker` works, run
  `docker run -d --name huntsman --restart unless-stopped -p 127.0.0.1:8080:8080 ghcr.io/tulipfarm/huntsman`
  and skip to step 3. If neither works, stop and tell the user to install WSL or Docker Desktop.
- **Anything else**: use the Docker command above if `docker` works. Otherwise stop and tell the user.

If `curl -fsS http://127.0.0.1:8080/health` already succeeds and `huntsman status` reports it running, Huntsman is
installed. Run `huntsman update` and skip to step 3.

## 2. Install Huntsman

```sh
curl -fsSL https://huntsman.tulipfarm.ai/install.sh | sh
```

This needs no sudo. It downloads Huntsman with its browser (Lightpanda) and ad and tracker filter lists into
`~/.huntsman`, checks the download's checksum, puts the `huntsman` command in `~/.local/bin`, and starts Huntsman as
a background service that starts again at login. It listens on `127.0.0.1:8080`, so only this machine can reach it.

If it fails:

- **"port 8080 is already in use"**: edit `~/.huntsman/env`, set `HUNTSMAN_ADDR=127.0.0.1:8787` (or another free
  port), run `~/.huntsman/bin/huntsman start`, and use that port in every URL below.
- **"too old for the bundled browser"** or an unsupported system: use the Docker command from step 1.
- Anything else: show the user the error and the output of `tail -n 30 ~/.huntsman/huntsman.log`, then stop.

Check that it works:

```sh
~/.huntsman/bin/huntsman status
curl -fsS http://127.0.0.1:8080/v1/scrape -H 'Content-Type: application/json' -d '{"url":"https://example.com"}'
```

The second command must print JSON whose `markdown` mentions "Example Domain".

## 3. Connect the user's AI tools

Huntsman's MCP server URL is `http://127.0.0.1:8080/mcp` (Streamable HTTP, no auth). Below, `URL` means that address.

Check for each tool below. Connect every one that is installed, using its command where one exists. Before editing a
JSON or TOML file, read it, keep everything already in it, and add only the `huntsman` entry. If a `huntsman` entry
already exists, leave it. Skip tools that aren't installed. Don't install them.

| Tool | Installed if | Connect with |
|---|---|---|
| Claude Code | `claude` command exists | `claude mcp add --transport http --scope user huntsman URL` |
| Codex | `codex` command exists | `codex mcp add huntsman --url URL` |
| GitHub Copilot CLI | `copilot` command exists | `copilot mcp add --transport http huntsman URL` |
| Gemini CLI | `gemini` command exists | `gemini mcp add --transport http --scope user huntsman URL` |
| VS Code (Copilot Chat) | `code` command exists | `code --add-mcp '{"name":"huntsman","type":"http","url":"URL"}'` |
| Cursor | `~/.cursor` exists | Add to `mcpServers` in `~/.cursor/mcp.json`: `"huntsman": {"url": "URL"}` |
| Windsurf | `~/.codeium/windsurf` exists | Add to `mcpServers` in `~/.codeium/windsurf/mcp_config.json`: `"huntsman": {"serverUrl": "URL"}` |
| Claude Desktop | its config folder exists (below) | Add to `mcpServers` in `claude_desktop_config.json`: `"huntsman": {"command": "npx", "args": ["-y", "mcp-remote", "URL"]}` |

Notes:

- If a CLI's `mcp add` command fails or doesn't exist in the installed version, add the server to its config file
  instead: Codex `~/.codex/config.toml` gets `[mcp_servers.huntsman]` with `url = "URL"`; Copilot CLI
  `~/.copilot/mcp-config.json` gets `"huntsman": {"type": "http", "url": "URL", "tools": ["*"]}` under
  `mcpServers`; Gemini CLI `~/.gemini/settings.json` gets `"huntsman": {"httpUrl": "URL"}` under `mcpServers`.
- Claude Desktop's config folder is `~/Library/Application Support/Claude` on macOS and `~/.config/Claude` on Linux.
  It reaches HTTP servers through `mcp-remote`, which needs Node.js (`npx`). Skip Claude Desktop if `npx` is missing,
  and tell the user why.
- If you are one of these tools, connect yourself too. Most tools load new MCP servers only after a restart.

## 4. Offer to make Huntsman the default (ask first)

Ask the user: "Should your agents use Huntsman instead of their built-in web fetch? I'd add one line to your global
agent instructions." Only if they agree, add this line to the global instruction file of each connected tool that
has one (for example `~/.claude/CLAUDE.md`, `~/.codex/AGENTS.md`, `~/.gemini/GEMINI.md`), creating the file if
needed:

> To read a web page, use the huntsman MCP server's `scrape` tool instead of the built-in web fetch.

## 5. Report back

Tell the user, briefly:

- Huntsman's version (`huntsman version`) and that it runs in the background at `http://127.0.0.1:8080`.
- Which tools you connected, which you skipped and why, and that they should restart those tools.
- Useful commands: `huntsman status`, `huntsman logs`, `huntsman update`, `huntsman uninstall`. Settings live in
  `~/.huntsman/env`.
- Guides: https://huntsman.tulipfarm.ai/install and https://huntsman.tulipfarm.ai/agents

## Tools Huntsman gives agents

| Tool | What it does |
|---|---|
| `scrape` | Reads one page and returns its main content as Markdown, plus its links. |
| `batch_scrape` | Starts reading a list of pages. Returns a job ID. |
| `crawl` | Starts following links from a page across a site. Returns a job ID. |
| `get_job` / `get_crawl` | Returns a batch or crawl's progress and results. |
