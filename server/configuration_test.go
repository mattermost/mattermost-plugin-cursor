package main

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testRegisterPath   = "/mattermost-ai/bridge/v1/mcp/register"
	testUnregisterPath = "/mattermost-ai/bridge/v1/mcp/unregister"
)

type registrationCall struct {
	path           string
	exposeExternal bool
}

type registrationRecorder struct {
	plugin.API

	mu    sync.Mutex
	calls []registrationCall
	load  func(dest any) error
}

func (r *registrationRecorder) LoadPluginConfiguration(dest any) error {
	return r.load(dest)
}

func (r *registrationRecorder) PluginHTTP(req *http.Request) *http.Response {
	body, _ := io.ReadAll(req.Body)
	var cfg struct {
		ExposeExternal bool `json:"expose_external"`
	}
	_ = json.Unmarshal(body, &cfg)
	r.mu.Lock()
	r.calls = append(r.calls, registrationCall{path: req.URL.Path, exposeExternal: cfg.ExposeExternal})
	r.mu.Unlock()
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}
}

func (r *registrationRecorder) snapshot() []registrationCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]registrationCall(nil), r.calls...)
}

func newActivatablePlugin(t *testing.T, exposed bool) (*Plugin, *registrationRecorder) {
	t.Helper()
	rec := &registrationRecorder{}
	rec.load = func(dest any) error {
		cfg, ok := dest.(*configuration)
		require.True(t, ok)
		cfg.ExposeMCPToolsPublicly = exposed
		return nil
	}
	p := &Plugin{}
	p.API = rec
	return p, rec
}

func TestExposeMCPToolsPubliclyDefaultsOff(t *testing.T) {
	assert.False(t, (&configuration{}).ExposeMCPToolsPublicly)

	p := &Plugin{}
	assert.False(t, p.getConfiguration().ExposeMCPToolsPublicly)

	var cfg configuration
	require.NoError(t, json.Unmarshal([]byte(`{"ServiceAccountAPIKey":"k"}`), &cfg))
	assert.False(t, cfg.ExposeMCPToolsPublicly)
}

func TestOnActivateRegistersWithConfiguredExposure(t *testing.T) {
	tests := []struct {
		name    string
		exposed bool
	}{
		{name: "default off", exposed: false},
		{name: "configured on", exposed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, rec := newActivatablePlugin(t, tt.exposed)
			require.NoError(t, p.OnActivate())
			t.Cleanup(func() { _ = p.OnDeactivate() })

			require.Eventually(t, func() bool { return len(rec.snapshot()) == 1 }, time.Second, 5*time.Millisecond)
			assert.Equal(t, registrationCall{path: testRegisterPath, exposeExternal: tt.exposed}, rec.snapshot()[0])
		})
	}
}

func TestConfigurationChangeReRegistersMCPServer(t *testing.T) {
	p, rec := newActivatablePlugin(t, false)
	require.NoError(t, p.OnActivate())
	require.Eventually(t, func() bool { return len(rec.snapshot()) == 1 }, time.Second, 5*time.Millisecond)

	exposed := true
	rec.load = func(dest any) error {
		dest.(*configuration).ExposeMCPToolsPublicly = exposed
		return nil
	}

	require.NoError(t, p.OnConfigurationChange())
	require.Eventually(t, func() bool { return len(rec.snapshot()) == 3 }, time.Second, 5*time.Millisecond)

	require.NoError(t, p.OnConfigurationChange())
	assert.Len(t, rec.snapshot(), 3, "unchanged exposure must not re-register")

	exposed = false
	require.NoError(t, p.OnConfigurationChange())
	require.Eventually(t, func() bool { return len(rec.snapshot()) == 5 }, time.Second, 5*time.Millisecond)

	assert.Equal(t, []registrationCall{
		{path: testRegisterPath, exposeExternal: false},
		{path: testUnregisterPath, exposeExternal: false},
		{path: testRegisterPath, exposeExternal: true},
		{path: testUnregisterPath, exposeExternal: true},
		{path: testRegisterPath, exposeExternal: false},
	}, rec.snapshot())

	require.NoError(t, p.OnDeactivate())
}

func TestConfigurationChangeBeforeActivationDoesNotRegister(t *testing.T) {
	p, rec := newActivatablePlugin(t, true)
	require.NoError(t, p.OnConfigurationChange())
	assert.Empty(t, rec.snapshot())
	assert.Nil(t, p.getMCPServer())
}
