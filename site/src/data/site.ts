export const repo = 'https://github.com/TulipFarm/huntsman';
export const dataset = 'https://huggingface.co/datasets/tulipfarmai/huntsman-scrape-dataset-v1';
export const image = 'ghcr.io/tulipfarm/huntsman';
export const mcpUrl = 'http://127.0.0.1:8080/mcp';
export const install = 'curl -fsSL https://huntsman.tulipfarm.ai/install.sh | sh';
export const agentPrompt = 'Read https://huntsman.tulipfarm.ai/install.md and install Huntsman for me.';
export const docker = `docker run -d --name huntsman --restart unless-stopped \\
  -p 127.0.0.1:8080:8080 -v huntsman-data:/data ${image}:latest`;

export const harnesses: { name: string; how: string; code: string; lang?: 'bash' | 'json' }[] = [
  { name: 'Claude Code', how: 'Run once in a terminal.', code: `claude mcp add --transport http --scope user huntsman ${mcpUrl}` },
  { name: 'Codex', how: 'Run once in a terminal.', code: `codex mcp add huntsman --url ${mcpUrl}` },
  { name: 'GitHub Copilot CLI', how: 'Run once in a terminal.', code: `copilot mcp add --transport http huntsman ${mcpUrl}` },
  {
    name: 'VS Code',
    how: 'Adds it to your user settings, for Copilot Chat in agent mode.',
    code: `code --add-mcp '{"name":"huntsman","type":"http","url":"${mcpUrl}"}'`,
  },
  {
    name: 'Cursor',
    how: 'Add to ~/.cursor/mcp.json, keeping any servers already there.',
    code: `{\n  "mcpServers": {\n    "huntsman": { "url": "${mcpUrl}" }\n  }\n}`,
    lang: 'json',
  },
  {
    name: 'Windsurf',
    how: 'Add to ~/.codeium/windsurf/mcp_config.json. Windsurf calls the field serverUrl.',
    code: `{\n  "mcpServers": {\n    "huntsman": { "serverUrl": "${mcpUrl}" }\n  }\n}`,
    lang: 'json',
  },
  { name: 'Gemini CLI', how: 'Run once in a terminal.', code: `gemini mcp add --transport http --scope user huntsman ${mcpUrl}` },
  {
    name: 'Claude Desktop',
    how: 'Add to claude_desktop_config.json (Settings → Developer → Edit Config). Claude Desktop reaches HTTP servers through mcp-remote, which needs Node.js.',
    code: `{\n  "mcpServers": {\n    "huntsman": {\n      "command": "npx",\n      "args": ["-y", "mcp-remote", "${mcpUrl}"]\n    }\n  }\n}`,
    lang: 'json',
  },
];
