package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-cursor/server/cursorapi"
)

func newConfig(t *testing.T, defaultModelID, defaultModelParams string) *configuration {
	t.Helper()
	config := &configuration{
		DefaultModelID:     defaultModelID,
		DefaultModelParams: defaultModelParams,
	}
	require.NoError(t, config.validate())
	return config
}

func TestResolveModel(t *testing.T) {
	tests := []struct {
		name    string
		input   createAgentInput
		config  *configuration
		want    *cursorapi.ModelRef
		wantErr string
	}{
		{
			name:   "nothing provided omits model",
			input:  createAgentInput{},
			config: newConfig(t, "", ""),
			want:   nil,
		},
		{
			name:   "backward compatible string model",
			input:  createAgentInput{Model: "composer-2"},
			config: newConfig(t, "", ""),
			want:   &cursorapi.ModelRef{ID: "composer-2"},
		},
		{
			name: "model with params",
			input: createAgentInput{
				Model:       "composer-2",
				ModelParams: []modelParamInput{{ID: "fast", Value: "true"}},
			},
			config: newConfig(t, "", ""),
			want: &cursorapi.ModelRef{
				ID:     "composer-2",
				Params: []cursorapi.ModelParam{{ID: "fast", Value: "true"}},
			},
		},
		{
			name:   "admin default used when agent omits model",
			input:  createAgentInput{},
			config: newConfig(t, "claude-4.6-sonnet-thinking", ""),
			want:   &cursorapi.ModelRef{ID: "claude-4.6-sonnet-thinking"},
		},
		{
			name:   "admin default model and params used when agent omits both",
			input:  createAgentInput{},
			config: newConfig(t, "composer-2", `[{"id":"fast","value":"true"}]`),
			want: &cursorapi.ModelRef{
				ID:     "composer-2",
				Params: []cursorapi.ModelParam{{ID: "fast", Value: "true"}},
			},
		},
		{
			name:   "admin default params applied when agent model matches default",
			input:  createAgentInput{Model: "composer-2"},
			config: newConfig(t, "composer-2", `[{"id":"fast","value":"true"}]`),
			want: &cursorapi.ModelRef{
				ID:     "composer-2",
				Params: []cursorapi.ModelParam{{ID: "fast", Value: "true"}},
			},
		},
		{
			name:   "admin default params not applied for a different model",
			input:  createAgentInput{Model: "claude-4.6-sonnet-thinking"},
			config: newConfig(t, "composer-2", `[{"id":"fast","value":"true"}]`),
			want:   &cursorapi.ModelRef{ID: "claude-4.6-sonnet-thinking"},
		},
		{
			name: "agent params override admin default params for the default model",
			input: createAgentInput{
				Model:       "composer-2",
				ModelParams: []modelParamInput{{ID: "fast", Value: "false"}},
			},
			config: newConfig(t, "composer-2", `[{"id":"fast","value":"true"}]`),
			want: &cursorapi.ModelRef{
				ID:     "composer-2",
				Params: []cursorapi.ModelParam{{ID: "fast", Value: "false"}},
			},
		},
		{
			name: "params without model resolve to admin default model",
			input: createAgentInput{
				ModelParams: []modelParamInput{{ID: "fast", Value: "true"}},
			},
			config: newConfig(t, "composer-2", ""),
			want: &cursorapi.ModelRef{
				ID:     "composer-2",
				Params: []cursorapi.ModelParam{{ID: "fast", Value: "true"}},
			},
		},
		{
			name: "params without model and no admin default is an error",
			input: createAgentInput{
				ModelParams: []modelParamInput{{ID: "fast", Value: "true"}},
			},
			config:  newConfig(t, "", ""),
			wantErr: "model_params require a model",
		},
		{
			name: "model param missing value is an error",
			input: createAgentInput{
				Model:       "composer-2",
				ModelParams: []modelParamInput{{ID: "fast"}},
			},
			config:  newConfig(t, "", ""),
			wantErr: "Each model parameter needs an id and a value",
		},
		{
			name: "duplicate model param ids are an error",
			input: createAgentInput{
				Model: "composer-2",
				ModelParams: []modelParamInput{
					{ID: "fast", Value: "true"},
					{ID: "fast", Value: "false"},
				},
			},
			config:  newConfig(t, "", ""),
			wantErr: "Model parameter IDs must be unique",
		},
		{
			name: "whitespace in model and params is trimmed",
			input: createAgentInput{
				Model:       "  composer-2  ",
				ModelParams: []modelParamInput{{ID: " fast ", Value: " true "}},
			},
			config: newConfig(t, "", ""),
			want: &cursorapi.ModelRef{
				ID:     "composer-2",
				Params: []cursorapi.ModelParam{{ID: "fast", Value: "true"}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ref, message := resolveModel(test.input, test.config)
			if test.wantErr != "" {
				assert.Nil(t, ref)
				assert.Contains(t, message, test.wantErr)
				return
			}
			assert.Empty(t, message)
			assert.Equal(t, test.want, ref)
		})
	}
}

// captureCreateAgent runs mcpCreateAgent against a stub that records the request body.
func captureCreateAgent(t *testing.T, config *configuration, input createAgentInput) (cursorapi.CreateAgentRequest, *mcp.CallToolResult) {
	t.Helper()
	var got cursorapi.CreateAgentRequest
	bodySeen := false
	p := newTestPlugin(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/agents", r.URL.Path)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &got))
		bodySeen = true
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{
			"agent":{"id":"bc-created","name":"Created","status":"ACTIVE","url":"https://cursor.com/agents/bc-created"},
			"run":{"id":"run-created","status":"CREATING"}
		}`)
	}))
	merged := *p.getConfiguration()
	merged.DefaultModelID = config.DefaultModelID
	merged.DefaultModelParams = config.DefaultModelParams
	merged.defaultModelParams = config.defaultModelParams
	p.setConfiguration(&merged)

	result, _, err := p.mcpCreateAgent(context.Background(), nil, input)
	require.NoError(t, err)
	if result != nil {
		require.False(t, bodySeen, "Cursor must not be called when resolution fails")
	}
	return got, result
}

func TestMCPCreateAgentSerializesModelObject(t *testing.T) {
	got, result := captureCreateAgent(t, newConfig(t, "", ""), createAgentInput{
		Prompt:      "do the thing",
		Repository:  "https://github.com/acme/repo",
		Model:       "composer-2",
		ModelParams: []modelParamInput{{ID: "fast", Value: "true"}},
	})
	require.Nil(t, result)
	require.NotNil(t, got.Model)
	assert.Equal(t, &cursorapi.ModelRef{
		ID:     "composer-2",
		Params: []cursorapi.ModelParam{{ID: "fast", Value: "true"}},
	}, got.Model)
}

func TestMCPCreateAgentUsesAdminDefaultModel(t *testing.T) {
	got, result := captureCreateAgent(t, newConfig(t, "composer-2", `[{"id":"fast","value":"true"}]`), createAgentInput{
		Prompt:     "do the thing",
		Repository: "https://github.com/acme/repo",
	})
	require.Nil(t, result)
	assert.Equal(t, &cursorapi.ModelRef{
		ID:     "composer-2",
		Params: []cursorapi.ModelParam{{ID: "fast", Value: "true"}},
	}, got.Model)
}

func TestMCPCreateAgentOmitsModelWhenUnset(t *testing.T) {
	got, result := captureCreateAgent(t, newConfig(t, "", ""), createAgentInput{
		Prompt:     "do the thing",
		Repository: "https://github.com/acme/repo",
	})
	require.Nil(t, result)
	assert.Nil(t, got.Model)
}

func TestMCPCreateAgentRejectsParamsWithoutModel(t *testing.T) {
	_, result := captureCreateAgent(t, newConfig(t, "", ""), createAgentInput{
		Prompt:      "do the thing",
		Repository:  "https://github.com/acme/repo",
		ModelParams: []modelParamInput{{ID: "fast", Value: "true"}},
	})
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	text, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, text.Text, "model_params require a model")
}

func TestConfigurationValidateRejectsMalformedParams(t *testing.T) {
	tests := []struct {
		name   string
		config configuration
		want   string
	}{
		{
			name:   "invalid json",
			config: configuration{DefaultModelID: "composer-2", DefaultModelParams: "not json"},
			want:   "expected a JSON array",
		},
		{
			name:   "object instead of array",
			config: configuration{DefaultModelID: "composer-2", DefaultModelParams: `{"id":"fast","value":"true"}`},
			want:   "expected a JSON array",
		},
		{
			name:   "unknown field",
			config: configuration{DefaultModelID: "composer-2", DefaultModelParams: `[{"id":"fast","value":"true","extra":"x"}]`},
			want:   "expected a JSON array",
		},
		{
			name:   "empty id",
			config: configuration{DefaultModelID: "composer-2", DefaultModelParams: `[{"id":"","value":"true"}]`},
			want:   "needs a non-empty id and value",
		},
		{
			name:   "duplicate id",
			config: configuration{DefaultModelID: "composer-2", DefaultModelParams: `[{"id":"fast","value":"true"},{"id":"fast","value":"false"}]`},
			want:   "duplicate Default Model Parameter id",
		},
		{
			name:   "params without model id",
			config: configuration{DefaultModelParams: `[{"id":"fast","value":"true"}]`},
			want:   "set a Default Model ID before adding Default Model Parameters",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.config.validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.want)
		})
	}
}

func TestConfigurationValidateAcceptsValidParams(t *testing.T) {
	config := configuration{
		DefaultModelID:     "  composer-2  ",
		DefaultModelParams: `[{"id":" fast ","value":" true "}]`,
	}
	require.NoError(t, config.validate())
	assert.Equal(t, "composer-2", config.DefaultModelID)
	assert.Equal(t, []cursorapi.ModelParam{{ID: "fast", Value: "true"}}, config.defaultModelParams)
}

func TestConfigurationSanitizeDropsInvalidParamsButKeepsOtherSettings(t *testing.T) {
	config := configuration{
		CursorAPIBaseURL:     "https://example.test",
		ServiceAccountAPIKey: "rotated-key",
		DefaultModelID:       "  composer-2  ",
		DefaultModelParams:   "not json",
	}
	err := config.sanitize()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected a JSON array")

	// Other settings survive, and the invalid params are dropped.
	assert.Equal(t, "https://example.test", config.CursorAPIBaseURL)
	assert.Equal(t, "rotated-key", config.ServiceAccountAPIKey)
	assert.Equal(t, "composer-2", config.DefaultModelID)
	assert.Empty(t, config.DefaultModelParams)
	assert.Nil(t, config.defaultModelParams)
}

func TestConfigurationSanitizeKeepsValidParams(t *testing.T) {
	config := configuration{
		DefaultModelID:     "composer-2",
		DefaultModelParams: `[{"id":"fast","value":"true"}]`,
	}
	require.NoError(t, config.sanitize())
	assert.Equal(t, []cursorapi.ModelParam{{ID: "fast", Value: "true"}}, config.defaultModelParams)
}
