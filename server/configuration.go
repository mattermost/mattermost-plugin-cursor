package main

import (
	"errors"
	"sync"

	"github.com/mattermost/mattermost-plugin-agents/v2/external/pluginmcp"
)

type configuration struct {
	CursorAPIBaseURL     string
	ServiceAccountAPIKey string
	// ExposeMCPToolsPublicly maps to pluginmcp.Config.ExposeExternal.
	ExposeMCPToolsPublicly bool
}

func (p *Plugin) OnConfigurationChange() error {
	var next configuration
	if err := p.API.LoadPluginConfiguration(&next); err != nil {
		return err
	}
	p.setConfiguration(&next)
	p.applyMCPExposure()
	return nil
}

func (p *Plugin) getConfiguration() *configuration {
	p.configurationLock.RLock()
	defer p.configurationLock.RUnlock()
	if p.configuration == nil {
		return &configuration{}
	}
	return p.configuration
}

func (p *Plugin) setConfiguration(next *configuration) {
	p.configurationLock.Lock()
	defer p.configurationLock.Unlock()
	if next == p.configuration {
		panic(errors.New("setConfiguration called with the existing configuration pointer"))
	}
	p.configuration = next
}

type configurationState struct {
	configurationLock sync.RWMutex
	configuration     *configuration
}

type mcpServerState struct {
	mcpLock           sync.RWMutex
	mcpServer         *pluginmcp.Server
	mcpExposeExternal bool

	// mcpApplyLock serializes re-registration and deactivation.
	mcpApplyLock sync.Mutex
}

func (p *Plugin) getMCPServer() *pluginmcp.Server {
	p.mcpLock.RLock()
	defer p.mcpLock.RUnlock()
	return p.mcpServer
}

func (p *Plugin) setMCPServer(server *pluginmcp.Server, exposeExternal bool) {
	p.mcpLock.Lock()
	defer p.mcpLock.Unlock()
	p.mcpServer = server
	p.mcpExposeExternal = exposeExternal
}

// applyMCPExposure replaces the registered MCP server when the exposure
// setting differs from the one it was registered with. It is a no-op before
// activation, where initMCPServer reads the setting itself.
func (p *Plugin) applyMCPExposure() {
	p.mcpApplyLock.Lock()
	defer p.mcpApplyLock.Unlock()

	p.mcpLock.RLock()
	current := p.mcpServer
	currentExposed := p.mcpExposeExternal
	p.mcpLock.RUnlock()

	want := p.getConfiguration().ExposeMCPToolsPublicly
	if current == nil || currentExposed == want {
		return
	}

	next := p.newMCPServer(want)
	p.setMCPServer(next, want)
	if err := current.Unregister(); err != nil {
		p.logError("Failed to unregister MCP server before re-registering", err)
	}
	if err := next.Register(); err != nil {
		p.logError("Failed to re-register MCP server after configuration change", err)
	}
}
