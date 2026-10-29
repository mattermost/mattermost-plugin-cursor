package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/external/pluginmcp"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

const (
	pluginID           = "com.mattermost.plugin-cursor"
	mcpBasePath        = "/mcp"
	agentsPluginID     = "mattermost-ai"
	guestDeniedMessage = "Guests cannot use Cursor Cloud Agents"
)

type Plugin struct {
	plugin.MattermostPlugin
	configurationState

	client       *pluginapi.Client
	mcpServer    *pluginmcp.Server
	httpClient   *http.Client
	hydration    hydrationCache
	lookupUser   func(userID string) (*model.User, error)
	getMCPUserID func(ctx context.Context) string
	recordAudit  func(rec *model.AuditRecord)
}

func (p *Plugin) OnActivate() error {
	p.client = pluginapi.NewClient(p.API, p.Driver)
	if err := p.OnConfigurationChange(); err != nil {
		return err
	}

	if p.httpClient == nil {
		p.httpClient = http.DefaultClient
	}
	p.hydration.initialize(hydrationCacheCapacity)
	p.initMCPServer()
	return p.mcpServer.Register()
}

func (p *Plugin) OnDeactivate() error {
	if p.mcpServer == nil {
		return nil
	}
	return p.mcpServer.Unregister()
}

func (p *Plugin) ServeHTTP(_ *plugin.Context, w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == mcpBasePath || strings.HasPrefix(r.URL.Path, mcpBasePath+"/") {
		userID := r.Header.Get("X-Mattermost-UserID")
		// Mattermost-Plugin-ID is server-set on PluginHTTP; X-Mattermost-UserID is not.
		if r.Header.Get("Mattermost-Plugin-ID") != agentsPluginID || userID == "" || p.isGuestUserID(userID) {
			writeError(w, http.StatusForbidden, guestDeniedMessage)
			return
		}
		if p.mcpServer == nil {
			http.Error(w, "MCP server is not initialized", http.StatusServiceUnavailable)
			return
		}
		p.mcpServer.ServeHTTP(w, r)
		return
	}
	http.NotFound(w, r)
}

func (p *Plugin) logError(message string, err error) {
	if p.client != nil {
		p.client.Log.Error(message, "error", err.Error())
	}
}

func (p *Plugin) logWarn(message string, keyValuePairs ...any) {
	if p.client != nil {
		p.client.Log.Warn(message, keyValuePairs...)
	}
}

func (p *Plugin) logInfo(message string, keyValuePairs ...any) {
	if p.client != nil {
		p.client.Log.Info(message, keyValuePairs...)
	}
}

func (p *Plugin) logDebug(message string, keyValuePairs ...any) {
	if p.client != nil {
		p.client.Log.Debug(message, keyValuePairs...)
	}
}

func (p *Plugin) getUser(userID string) (*model.User, error) {
	if p.lookupUser != nil {
		return p.lookupUser(userID)
	}
	if p.API == nil {
		return nil, errors.New("user lookup is unavailable")
	}
	user, appErr := p.API.GetUser(userID)
	if appErr != nil {
		return nil, appErr
	}
	return user, nil
}

func (p *Plugin) isGuestUserID(userID string) bool {
	user, err := p.getUser(userID)
	if err != nil {
		p.logError("Failed to look up user for guest check", err)
		return true
	}
	if user == nil {
		return true
	}
	return user.IsGuest()
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
