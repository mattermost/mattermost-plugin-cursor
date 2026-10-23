# Cursor Cloud Agents Plugin for Mattermost

This plugin exposes Cursor Cloud Agents tools to the Mattermost Agents plugin via MCP.

An administrator configures the Service Account API Key in the Mattermost System Console. This key powers every MCP tool call, so invoking users do not need personal Cursor access. Create the service account key in the Cursor Dashboard.

## MCP tools

- `create_agent`
- `get_agent`
- `list_agents`
- `list_environments`
- `add_followup`
- `get_agent_conversation`

## Build and develop

```bash
make dist
go test ./server/...
make deploy
```

The plugin requires Mattermost 11.3 or newer.
