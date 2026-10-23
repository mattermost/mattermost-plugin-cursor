package main

import (
	"context"

	"github.com/mattermost/mattermost-plugin-agents/v2/external/pluginmcp"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	auditEventMCPCreateAgent          = "mcpCreateAgent"
	auditEventMCPGetAgent             = "mcpGetAgent"
	auditEventMCPListAgents           = "mcpListAgents"
	auditEventMCPAddFollowup          = "mcpAddFollowup"
	auditEventMCPGetAgentConversation = "mcpGetAgentConversation"
	auditMCPObjectType                = "cursor_cloud_agent"
	auditMCPClient                    = "mattermost-agents-mcp"
	missingMCPUserMessage             = "Unable to identify the Mattermost user for this request"
)

func (p *Plugin) actingUserID(ctx context.Context) string {
	if p.getMCPUserID != nil {
		return p.getMCPUserID(ctx)
	}
	return pluginmcp.GetUserID(ctx)
}

func (p *Plugin) beginMCPAudit(ctx context.Context, event, tool string) (*model.AuditRecord, *mcp.CallToolResult) {
	rec := plugin.MakeAuditRecord(event, model.AuditStatusFail)
	rec.AddEventObjectType(auditMCPObjectType)
	rec.Actor.Client = auditMCPClient
	rec.AddMeta(model.AuditKeyAPIPath, mcpBasePath)
	model.AddEventParameterToAuditRec(rec, "tool", tool)

	userID := p.actingUserID(ctx)
	rec.Actor.UserId = userID
	if userID == "" {
		p.logWarn("MCP tool invoked without a Mattermost user ID", "tool", tool)
		rec.AddErrorDesc("missing acting user ID")
		return rec, toolError(missingMCPUserMessage)
	}
	model.AddEventParameterToAuditRec(rec, "user_id", userID)
	return rec, nil
}

func (p *Plugin) finishMCPAudit(rec *model.AuditRecord) {
	if rec == nil {
		return
	}
	if p.recordAudit != nil {
		p.recordAudit(rec)
	}
	if p.API != nil {
		p.API.LogAuditRec(rec)
	}

	userID := rec.Actor.UserId
	if userID == "" {
		return
	}
	pairs := []any{
		"event", rec.EventName,
		"tool", rec.EventData.Parameters["tool"],
		"user_id", userID,
		"status", rec.Status,
	}
	for _, key := range []string{"agent_id", "repository", "ref", "repositories", "refs", "limit", "model"} {
		value, ok := rec.EventData.Parameters[key]
		if !ok {
			continue
		}
		pairs = append(pairs, key, value)
	}
	if rec.Status == model.AuditStatusSuccess {
		p.logInfo("MCP tool invoked", pairs...)
		return
	}
	p.logWarn("MCP tool failed", pairs...)
}
