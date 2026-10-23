package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mattermost/mattermost-plugin-agents/v2/external/pluginmcp"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattermost/mattermost-plugin-cursor/server/cursorapi"
)

const (
	conversationCharacterBudget = 20_000
	serviceAccountCachePrefix   = "service-account:"
)

type createAgentInput struct {
	Prompt       string `json:"prompt" jsonschema:"Task for the autonomous Cursor Cloud agent,minLength=1"`
	Repository   string `json:"repository" jsonschema:"GitHub repository URL such as https://github.com/org/repo,minLength=1"`
	Ref          string `json:"ref,omitempty" jsonschema:"Optional starting branch or commit SHA"`
	Model        string `json:"model,omitempty" jsonschema:"Optional Cursor model ID"`
	AutoCreatePR *bool  `json:"auto_create_pr,omitempty" jsonschema:"Whether Cursor should open a pull request when work completes"`
}

type createAgentOutput struct {
	AgentID string `json:"agent_id"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	URL     string `json:"url"`
}

type getAgentInput struct {
	AgentID string `json:"agent_id" jsonschema:"Cursor Cloud agent ID,minLength=1"`
}

type getAgentOutput struct {
	AgentID string `json:"agent_id"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Summary string `json:"summary"`
	Branch  string `json:"branch"`
	PRURL   string `json:"pr_url"`
	URL     string `json:"url"`
}

type listAgentsInput struct {
	Limit *int `json:"limit,omitempty" jsonschema:"Maximum agents to return,minimum=1,maximum=100"`
}

type listedAgent struct {
	AgentID    string `json:"agent_id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Repository string `json:"repository"`
	CreatedAt  string `json:"created_at"`
}

type listAgentsOutput struct {
	Agents []listedAgent `json:"agents"`
}

type addFollowupInput struct {
	AgentID string `json:"agent_id" jsonschema:"Cursor Cloud agent ID,minLength=1"`
	Prompt  string `json:"prompt" jsonschema:"Follow-up instruction for the agent,minLength=1"`
}

type addFollowupOutput struct {
	RunID  string `json:"run_id"`
	Status string `json:"status"`
}

type getAgentConversationInput struct {
	AgentID string `json:"agent_id" jsonschema:"Cursor Cloud agent ID,minLength=1"`
}

type conversationMessage struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type getAgentConversationOutput struct {
	Messages  []conversationMessage `json:"messages"`
	Truncated bool                  `json:"truncated,omitempty"`
}

func (p *Plugin) initMCPServer() {
	exposeExternal := p.getConfiguration().ExposeMCPToolsPublicly
	p.setMCPServer(p.newMCPServer(exposeExternal), exposeExternal)
}

func (p *Plugin) newMCPServer(exposeExternal bool) *pluginmcp.Server {
	version := "0.0.1"
	if manifest != nil && manifest.Version != "" {
		version = manifest.Version
	}
	server := pluginmcp.NewServer(p.API, pluginmcp.Config{
		PluginID:       pluginID,
		Name:           "Cursor Cloud Agents",
		Path:           mcpBasePath,
		ExposeExternal: exposeExternal,
		Version:        version,
	})
	pluginmcp.AddTool(server, &mcp.Tool{
		Name: "create_agent",
		Description: "Launch an autonomous Cursor Cloud agent on a repository and return immediately. " +
			"The agent runs asynchronously; poll it with get_agent.",
	}, p.mcpCreateAgent)
	pluginmcp.AddTool(server, &mcp.Tool{
		Name:        "get_agent",
		Description: "Get the current state and latest result of a Cursor Cloud agent.",
	}, p.mcpGetAgent)
	pluginmcp.AddTool(server, &mcp.Tool{
		Name:        "list_agents",
		Description: "List recent Cursor Cloud agents available to the configured service account.",
	}, p.mcpListAgents)
	pluginmcp.AddTool(server, &mcp.Tool{
		Name: "add_followup",
		Description: "Send a follow-up instruction to a Cursor Cloud agent. " +
			"This fails with a conflict if the agent is still running.",
	}, p.mcpAddFollowup)
	pluginmcp.AddTool(server, &mcp.Tool{
		Name:        "get_agent_conversation",
		Description: "Return recent messages from a Cursor Cloud agent conversation.",
	}, p.mcpGetAgentConversation)
	return server
}

func (p *Plugin) mcpCreateAgent(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input createAgentInput,
) (*mcp.CallToolResult, createAgentOutput, error) {
	rec, failure := p.beginMCPAudit(ctx, auditEventMCPCreateAgent, "create_agent")
	defer p.finishMCPAudit(rec)
	if failure != nil {
		return failure, createAgentOutput{}, nil
	}
	model.AddEventParameterToAuditRec(rec, "repository", input.Repository)
	if input.Ref != "" {
		model.AddEventParameterToAuditRec(rec, "ref", input.Ref)
	}
	if input.Model != "" {
		model.AddEventParameterToAuditRec(rec, "model", input.Model)
	}

	client, _, failure := p.mcpClient()
	if failure != nil {
		rec.AddErrorDesc("service account API key is not configured")
		return failure, createAgentOutput{}, nil
	}
	request := cursorapi.NewCreateAgentRequest(
		input.Prompt,
		input.Repository,
		input.Ref,
		input.Model,
		input.AutoCreatePR,
	)
	response, err := client.CreateAgent(ctx, request)
	if err != nil {
		rec.AddErrorDesc(err.Error())
		return toolError(err.Error()), createAgentOutput{}, nil
	}
	created, err := cursorapi.Decode[cursorapi.CreateAgentResponse](response)
	if err != nil {
		rec.AddErrorDesc(err.Error())
		return cursorToolError(response, err), createAgentOutput{}, nil
	}
	model.AddEventParameterToAuditRec(rec, "agent_id", created.Agent.ID)
	rec.Success()
	return nil, createAgentOutput{
		AgentID: created.Agent.ID,
		Name:    created.Agent.Name,
		Status:  created.Run.Status,
		URL:     created.Agent.URL,
	}, nil
}

func (p *Plugin) mcpGetAgent(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input getAgentInput,
) (*mcp.CallToolResult, getAgentOutput, error) {
	rec, failure := p.beginMCPAudit(ctx, auditEventMCPGetAgent, "get_agent")
	defer p.finishMCPAudit(rec)
	if failure != nil {
		return failure, getAgentOutput{}, nil
	}
	model.AddEventParameterToAuditRec(rec, "agent_id", input.AgentID)

	client, cacheIdentity, failure := p.mcpClient()
	if failure != nil {
		rec.AddErrorDesc("service account API key is not configured")
		return failure, getAgentOutput{}, nil
	}
	response, err := client.GetAgent(ctx, input.AgentID)
	if err != nil {
		rec.AddErrorDesc(err.Error())
		return toolError(err.Error()), getAgentOutput{}, nil
	}
	agent, err := cursorapi.Decode[cursorapi.Agent](response)
	if err != nil {
		rec.AddErrorDesc(err.Error())
		return cursorToolError(response, err), getAgentOutput{}, nil
	}
	p.hydration.putAgent(cacheIdentity, agent)
	output := getAgentOutput{
		AgentID: agent.ID,
		Name:    agent.Name,
		Status:  agent.Status,
		URL:     agent.URL,
	}
	if agent.LatestRunID != "" {
		hydrated, ok := p.hydrateAgent(ctx, cacheIdentity, client, agent.AgentSummary, true)
		if ok {
			output.Status = hydrated.RunStatus
			output.Summary = hydrated.Result
		}
		output.Branch = hydrated.Branch
		output.PRURL = hydrated.PRURL
	}
	rec.Success()
	return nil, output, nil
}

func (p *Plugin) mcpListAgents(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input listAgentsInput,
) (*mcp.CallToolResult, listAgentsOutput, error) {
	rec, failure := p.beginMCPAudit(ctx, auditEventMCPListAgents, "list_agents")
	defer p.finishMCPAudit(rec)
	if failure != nil {
		return failure, listAgentsOutput{}, nil
	}
	if input.Limit != nil {
		model.AddEventParameterToAuditRec(rec, "limit", *input.Limit)
	}

	client, cacheIdentity, failure := p.mcpClient()
	if failure != nil {
		rec.AddErrorDesc("service account API key is not configured")
		return failure, listAgentsOutput{}, nil
	}
	query := make(url.Values)
	if input.Limit != nil {
		query.Set("limit", strconv.Itoa(*input.Limit))
	}
	response, err := client.ListAgents(ctx, query)
	if err != nil {
		rec.AddErrorDesc(err.Error())
		return toolError(err.Error()), listAgentsOutput{}, nil
	}
	list, err := cursorapi.Decode[cursorapi.ListAgentsResponse](response)
	if err != nil {
		rec.AddErrorDesc(err.Error())
		return cursorToolError(response, err), listAgentsOutput{}, nil
	}
	hydrated := p.hydrateAgents(ctx, cacheIdentity, client, list.Items, false)
	output := listAgentsOutput{Agents: make([]listedAgent, 0, len(hydrated))}
	for _, agent := range hydrated {
		repository := ""
		if len(agent.Repos) > 0 {
			repository = agent.Repos[0].URL
		}
		output.Agents = append(output.Agents, listedAgent{
			AgentID:    agent.ID,
			Name:       agent.Name,
			Status:     agent.Status,
			Repository: repository,
			CreatedAt:  agent.CreatedAt,
		})
	}
	rec.Success()
	return nil, output, nil
}

func (p *Plugin) mcpAddFollowup(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input addFollowupInput,
) (*mcp.CallToolResult, addFollowupOutput, error) {
	rec, failure := p.beginMCPAudit(ctx, auditEventMCPAddFollowup, "add_followup")
	defer p.finishMCPAudit(rec)
	if failure != nil {
		return failure, addFollowupOutput{}, nil
	}
	model.AddEventParameterToAuditRec(rec, "agent_id", input.AgentID)

	client, _, failure := p.mcpClient()
	if failure != nil {
		rec.AddErrorDesc("service account API key is not configured")
		return failure, addFollowupOutput{}, nil
	}
	response, err := client.CreateRun(ctx, input.AgentID, cursorapi.CreateRunRequest{
		Prompt: cursorapi.Prompt{Text: input.Prompt},
	})
	if err != nil {
		rec.AddErrorDesc(err.Error())
		return toolError(err.Error()), addFollowupOutput{}, nil
	}
	created, err := cursorapi.Decode[cursorapi.CreateRunResponse](response)
	if err != nil {
		rec.AddErrorDesc(err.Error())
		return cursorToolError(response, err), addFollowupOutput{}, nil
	}
	rec.Success()
	return nil, addFollowupOutput{RunID: created.Run.ID, Status: created.Run.Status}, nil
}

func (p *Plugin) mcpGetAgentConversation(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input getAgentConversationInput,
) (*mcp.CallToolResult, getAgentConversationOutput, error) {
	rec, failure := p.beginMCPAudit(ctx, auditEventMCPGetAgentConversation, "get_agent_conversation")
	defer p.finishMCPAudit(rec)
	if failure != nil {
		return failure, getAgentConversationOutput{}, nil
	}
	model.AddEventParameterToAuditRec(rec, "agent_id", input.AgentID)

	client, _, failure := p.mcpClient()
	if failure != nil {
		rec.AddErrorDesc("service account API key is not configured")
		return failure, getAgentConversationOutput{}, nil
	}
	response, err := client.GetConversation(ctx, input.AgentID)
	if err != nil {
		rec.AddErrorDesc(err.Error())
		return toolError(err.Error()), getAgentConversationOutput{}, nil
	}
	conversation, err := cursorapi.Decode[cursorapi.ConversationResponse](response)
	if err != nil {
		rec.AddErrorDesc(err.Error())
		return cursorToolError(response, err), getAgentConversationOutput{}, nil
	}
	output := truncateMessages(conversation.Messages, conversationCharacterBudget)
	rec.Success()
	return nil, output, nil
}

func (p *Plugin) mcpClient() (*cursorapi.Client, string, *mcp.CallToolResult) {
	config := p.getConfiguration()
	apiKey := strings.TrimSpace(config.ServiceAccountAPIKey)
	if apiKey == "" {
		return nil, "", toolError(
			"A Mattermost administrator must configure the Cursor Service Account API Key " +
				"in the System Console before Cursor tools can be used.",
		)
	}
	fingerprint := sha256.Sum256([]byte(apiKey))
	cacheIdentity := fmt.Sprintf("%s%x", serviceAccountCachePrefix, fingerprint[:8])
	return cursorapi.NewClient(config.CursorAPIBaseURL, apiKey, p.httpClient), cacheIdentity, nil
}

func truncateMessages(messages []cursorapi.Message, budget int) getAgentConversationOutput {
	remaining := budget
	reversed := make([]conversationMessage, 0, len(messages))
	truncated := false
	for _, message := range slices.Backward(messages) {
		if remaining == 0 {
			truncated = true
			break
		}
		text := message.Text
		if len(text) > remaining {
			offset := len(text) - remaining
			for offset < len(text) && !utf8.RuneStart(text[offset]) {
				offset++
			}
			text = text[offset:]
			truncated = true
			remaining = 0
		} else {
			remaining -= len(text)
		}
		reversed = append(reversed, conversationMessage{Type: message.Type, Text: text})
	}
	slices.Reverse(reversed)
	output := getAgentConversationOutput{
		Messages:  reversed,
		Truncated: truncated,
	}
	return output
}

func cursorToolError(response cursorapi.Response, err error) *mcp.CallToolResult {
	var apiErr *cursorapi.APIError
	if errors.As(err, &apiErr) {
		var upstream struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(response.Body, &upstream) == nil && upstream.Error.Message != "" {
			return toolError(upstream.Error.Message)
		}
		return toolError(fmt.Sprintf("Cursor API returned status %d", response.StatusCode))
	}
	return toolError("Cursor returned an invalid response")
}

func toolError(message string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: message}},
		IsError: true,
	}
}
