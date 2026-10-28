package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/mattermost/mattermost-plugin-cursor/server/cursorapi"
)

type configuration struct {
	CursorAPIBaseURL     string
	ServiceAccountAPIKey string
	// DefaultModelID is an optional Cursor model ID applied when a calling agent
	// does not specify one. Empty means no default.
	DefaultModelID string
	// DefaultModelParams is the raw JSON array of {"id","value"} the admin enters
	// in the System Console. It is parsed and validated into defaultModelParams.
	DefaultModelParams string

	// defaultModelParams is the parsed form of DefaultModelParams.
	defaultModelParams []cursorapi.ModelParam
}

func (p *Plugin) OnConfigurationChange() error {
	var next configuration
	if err := p.API.LoadPluginConfiguration(&next); err != nil {
		return err
	}
	if err := next.validate(); err != nil {
		p.logWarn("Rejected invalid Cursor plugin configuration", "error", err.Error())
		return err
	}
	p.setConfiguration(&next)
	return nil
}

// validate parses and checks admin-provided defaults, failing with a helpful
// message when the input is malformed so the System Console surfaces it on save.
func (c *configuration) validate() error {
	c.DefaultModelID = strings.TrimSpace(c.DefaultModelID)
	params, err := parseDefaultModelParams(c.DefaultModelParams)
	if err != nil {
		return err
	}
	if len(params) > 0 && c.DefaultModelID == "" {
		return errors.New(
			"set a Default Model ID before adding Default Model Parameters, " +
				"or clear the parameters",
		)
	}
	c.defaultModelParams = params
	return nil
}

// parseDefaultModelParams parses the admin JSON for default model params. It
// accepts an empty string (no defaults) or a JSON array of {"id","value"}.
func parseDefaultModelParams(raw string) ([]cursorapi.ModelParam, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var params []cursorapi.ModelParam
	if err := decoder.Decode(&params); err != nil {
		return nil, fmt.Errorf(
			"invalid Default Model Parameters, expected a JSON array like "+
				`[{"id":"thinking","value":"high"}]: %w`, err)
	}
	seen := make(map[string]struct{}, len(params))
	for _, param := range params {
		id := strings.TrimSpace(param.ID)
		if id == "" || strings.TrimSpace(param.Value) == "" {
			return nil, errors.New("each Default Model Parameter needs a non-empty id and value")
		}
		if _, dup := seen[id]; dup {
			return nil, fmt.Errorf("duplicate Default Model Parameter id %q", id)
		}
		seen[id] = struct{}{}
	}
	return params, nil
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
