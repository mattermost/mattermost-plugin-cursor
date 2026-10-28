# Cursor Cloud Agents Plugin for Mattermost

This plugin exposes Cursor Cloud Agents tools to the Mattermost Agents plugin via MCP.

An administrator configures the Service Account API Key in the Mattermost System Console. This key powers every MCP tool call, so invoking users do not need personal Cursor access. Create the service account key in the Cursor Dashboard.

## MCP tools

- `create_agent`
- `get_agent`
- `list_agents`
- `list_environments`
- `list_models`
- `add_followup`
- `get_agent_conversation`

### Choosing a model

`create_agent` accepts an optional `model` (a Cursor model ID string) and an optional `model_params` array of `{ "id", "value" }` entries (for example reasoning effort or context window). Both are optional and backward compatible: existing calls that pass only `model` keep working.

Call `list_models` to discover the valid model IDs, each model's tunable parameters and their allowed values, and the concrete variants it accepts, then pass a valid `model`/`model_params` to `create_agent`. The plugin validates the shape of `model_params` (each needs an id and value, ids must be unique) and otherwise passes values through to Cursor, surfacing the Cursor API error if a value is not permitted. This keeps the plugin from going stale as Cursor's model catalog changes.

The model sent to Cursor resolves in this order:

1. The `model` (and `model_params`) explicitly provided in the `create_agent` call.
2. The admin-configured **Default Model ID** (and **Default Model Parameters**), if set.
3. Nothing: `model` is omitted from the request so Cursor applies the user default, then team default, then system default.

Edge cases:

- If the call sets `model` but no `model_params`, the admin **Default Model Parameters** are applied only when the requested model ID equals the **Default Model ID**, so params are never paired with a model they may not be valid for.
- If the call sets `model_params` but no `model`, the model ID resolves to the admin **Default Model ID** when configured; otherwise the call fails with a clear error that params require a model.

## System Console settings

- **Service Account API Key** — powers every MCP tool call.
- **Cursor API Base URL** — override for testing; defaults to `https://api.cursor.com`.
- **Default Model ID** — optional Cursor model ID used when the Agents bot does not request one. Leave empty to defer to Cursor's own defaults.
- **Default Model Parameters** — optional JSON array of `{ "id", "value" }` applied with the Default Model ID, for example `[{"id":"thinking","value":"high"}]`. Requires a Default Model ID. Invalid JSON (or params without a Default Model ID) is rejected on save with a helpful message.

## Build and develop

```bash
make dist
go test ./server/...
make deploy
```

The plugin requires Mattermost 11.3 or newer.
