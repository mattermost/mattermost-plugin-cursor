package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-cursor/server/cursorapi"
)

func TestServeHTTPRejectsGuestsOnMCP(t *testing.T) {
	p := newTestPlugin(t, http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("upstream should not be called")
	}))
	defaultLookup := p.lookupUser

	tests := []struct {
		name       string
		userID     string
		pluginID   string
		lookup     func(string) (*model.User, error)
		wantStatus int
		wantBody   string
	}{
		{
			name:     "guest",
			userID:   "guest-1",
			pluginID: agentsPluginID,
			lookup: func(userID string) (*model.User, error) {
				return &model.User{Id: userID, Roles: model.SystemGuestRoleId}, nil
			},
			wantStatus: http.StatusForbidden,
			wantBody:   `{"error":"Guests cannot use Cursor Cloud Agents"}`,
		},
		{
			name:     "lookup error",
			userID:   "user-1",
			pluginID: agentsPluginID,
			lookup: func(string) (*model.User, error) {
				return nil, errors.New("lookup failed")
			},
			wantStatus: http.StatusForbidden,
			wantBody:   `{"error":"Guests cannot use Cursor Cloud Agents"}`,
		},
		{
			name:       "missing user",
			pluginID:   agentsPluginID,
			wantStatus: http.StatusForbidden,
			wantBody:   `{"error":"Guests cannot use Cursor Cloud Agents"}`,
		},
		{
			name:       "spoofed user ID without plugin ID",
			userID:     "user-1",
			wantStatus: http.StatusForbidden,
			wantBody:   `{"error":"Guests cannot use Cursor Cloud Agents"}`,
		},
		{
			name:       "spoofed user ID with wrong plugin ID",
			userID:     "user-1",
			pluginID:   "com.evil.plugin",
			wantStatus: http.StatusForbidden,
			wantBody:   `{"error":"Guests cannot use Cursor Cloud Agents"}`,
		},
		{
			name:       "regular user reaches MCP server",
			userID:     "user-1",
			pluginID:   agentsPluginID,
			wantStatus: http.StatusServiceUnavailable,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.lookup != nil {
				p.lookupUser = test.lookup
				t.Cleanup(func() { p.lookupUser = defaultLookup })
			}
			request := httptest.NewRequest(http.MethodPost, mcpBasePath, nil)
			if test.userID != "" {
				request.Header.Set("X-Mattermost-UserID", test.userID)
			}
			if test.pluginID != "" {
				request.Header.Set("Mattermost-Plugin-ID", test.pluginID)
			}
			recorder := httptest.NewRecorder()
			p.ServeHTTP(nil, recorder, request)
			assert.Equal(t, test.wantStatus, recorder.Code)
			if test.wantBody != "" {
				assert.JSONEq(t, test.wantBody, recorder.Body.String())
			}
		})
	}
}

func happyPathCursorAPI(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer "+testServiceAccountAPIKey, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/agents":
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{
				"agent":{"id":"bc-created","name":"Created","status":"ACTIVE","url":"https://cursor.com/agents/bc-created"},
				"run":{"id":"run-created","status":"CREATING"}
			}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/agents/bc-get":
			_, _ = io.WriteString(w, `{
				"id":"bc-get","name":"Get agent","status":"ACTIVE",
				"env":{"type":"cloud"},"url":"https://cursor.com/agents/bc-get",
				"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:01:00Z",
				"latestRunId":"run-latest","repos":[{"url":"https://github.com/acme/repo"}],
				"workOnCurrentBranch":false,"autoCreatePR":true
			}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/agents/bc-get/runs/run-latest":
			_, _ = io.WriteString(w, `{
				"id":"run-latest","status":"FINISHED","result":"Implemented the change",
				"git":{"branches":[{"branch":"cursor/change","prUrl":"https://github.com/acme/repo/pull/1"}]}
			}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/environments":
			_, _ = io.WriteString(w, `{"items":[{
				"id":"env-1","name":"Web app","owner":"team",
				"repos":[
					{"url":"https://github.com/acme/web"},
					{"url":"https://github.com/acme/api"}
				],
				"createdAt":"2026-09-01T16:20:00.000Z",
				"updatedAt":"2026-09-29T21:05:00.000Z"
			}],"nextCursor":"MjA"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/agents":
			_, _ = io.WriteString(w, `{"items":[{
				"id":"bc-list","name":"Listed","status":"ACTIVE",
				"env":{"type":"cloud"},"url":"https://cursor.com/agents/bc-list",
				"createdAt":"2026-01-02T03:04:05Z","updatedAt":"2026-01-02T03:05:05Z"
			}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/agents/bc-list":
			_, _ = io.WriteString(w, `{
				"id":"bc-list","name":"Listed","status":"ACTIVE",
				"env":{"type":"cloud"},"url":"https://cursor.com/agents/bc-list",
				"createdAt":"2026-01-02T03:04:05Z","updatedAt":"2026-01-02T03:05:05Z",
				"repos":[{"url":"https://github.com/acme/repo"},{"url":"https://github.com/acme/web"}],
				"workOnCurrentBranch":false,"autoCreatePR":false
			}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/agents/bc-follow/runs":
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"run":{"id":"run-follow","status":"CREATING"}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			_, _ = io.WriteString(w, `{"items":[
				{
					"id":"composer-2","displayName":"Composer 2","aliases":["composer-latest"],
					"parameters":[{"id":"fast","displayName":"Fast","values":[
						{"value":"false"},{"value":"true","displayName":"Fast"}
					]}],
					"variants":[
						{"params":[{"id":"fast","value":"true"}],"displayName":"Composer 2","isDefault":true},
						{"params":[{"id":"fast","value":"false"}],"displayName":"Composer 2"}
					]
				},
				{
					"id":"claude-4.6-sonnet-thinking","displayName":"Claude 4.6 Sonnet (Thinking)",
					"variants":[{"params":[],"displayName":"Claude 4.6 Sonnet (Thinking)","isDefault":true}]
				}
			]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v0/agents/bc-conversation/conversation":
			_, _ = io.WriteString(w, `{"id":"bc-conversation","messages":[
				{"type":"user_message","text":"Please fix it"},
				{"type":"assistant_message","text":"Done"}
			]}`)
		default:
			http.NotFound(w, r)
		}
	})
}

func TestMCPToolHandlersHappyPath(t *testing.T) {
	p := newTestPlugin(t, happyPathCursorAPI(t))
	ctx := context.Background()

	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "create_agent",
			run: func(t *testing.T) {
				result, output, err := p.mcpCreateAgent(ctx, nil, createAgentInput{
					Prompt: "make a change",
					Repositories: []repositoryInput{{
						URL: "https://github.com/acme/repo",
					}},
				})
				require.NoError(t, err)
				assert.Nil(t, result)
				assert.Equal(t, createAgentOutput{
					AgentID: "bc-created",
					Name:    "Created",
					Status:  "CREATING",
					URL:     "https://cursor.com/agents/bc-created",
				}, output)
			},
		},
		{
			name: "get_agent",
			run: func(t *testing.T) {
				result, output, err := p.mcpGetAgent(ctx, nil, getAgentInput{AgentID: "bc-get"})
				require.NoError(t, err)
				assert.Nil(t, result)
				assert.Equal(t, getAgentOutput{
					AgentID: "bc-get",
					Name:    "Get agent",
					Status:  "FINISHED",
					Summary: "Implemented the change",
					Branch:  "cursor/change",
					PRURL:   "https://github.com/acme/repo/pull/1",
					URL:     "https://cursor.com/agents/bc-get",
				}, output)
			},
		},
		{
			name: "list_agents",
			run: func(t *testing.T) {
				limit := 5
				result, output, err := p.mcpListAgents(ctx, nil, listAgentsInput{Limit: &limit})
				require.NoError(t, err)
				assert.Nil(t, result)
				require.Len(t, output.Agents, 1)
				assert.Equal(t, listedAgent{
					AgentID:    "bc-list",
					Name:       "Listed",
					Status:     "ACTIVE",
					Repository: "https://github.com/acme/repo",
					Repositories: []string{
						"https://github.com/acme/repo",
						"https://github.com/acme/web",
					},
					CreatedAt: "2026-01-02T03:04:05Z",
				}, output.Agents[0])
			},
		},
		{
			name: "list_environments",
			run: func(t *testing.T) {
				result, output, err := p.mcpListEnvironments(ctx, nil, listEnvironmentsInput{})
				require.NoError(t, err)
				assert.Nil(t, result)
				assert.Equal(t, listEnvironmentsOutput{
					Environments: []listedEnvironment{{
						ID:    "env-1",
						Name:  "Web app",
						Owner: "team",
						Repositories: []string{
							"https://github.com/acme/web",
							"https://github.com/acme/api",
						},
						UpdatedAt: "2026-09-29T21:05:00.000Z",
					}},
					NextCursor: "MjA",
				}, output)
			},
		},
		{
			name: "add_followup",
			run: func(t *testing.T) {
				result, output, err := p.mcpAddFollowup(ctx, nil, addFollowupInput{
					AgentID: "bc-follow",
					Prompt:  "also update tests",
				})
				require.NoError(t, err)
				assert.Nil(t, result)
				assert.Equal(t, addFollowupOutput{RunID: "run-follow", Status: "CREATING"}, output)
			},
		},
		{
			name: "get_agent_conversation",
			run: func(t *testing.T) {
				result, output, err := p.mcpGetAgentConversation(
					ctx,
					nil,
					getAgentConversationInput{AgentID: "bc-conversation"},
				)
				require.NoError(t, err)
				assert.Nil(t, result)
				assert.Equal(t, getAgentConversationOutput{
					Messages: []conversationMessage{
						{Type: "user_message", Text: "Please fix it"},
						{Type: "assistant_message", Text: "Done"},
					},
				}, output)
			},
		},
		{
			name: "list_models",
			run: func(t *testing.T) {
				result, output, err := p.mcpListModels(ctx, nil, listModelsInput{})
				require.NoError(t, err)
				assert.Nil(t, result)
				assert.Equal(t, listModelsOutput{Models: []listedModel{
					{
						ID:          "composer-2",
						DisplayName: "Composer 2",
						Aliases:     []string{"composer-latest"},
						Parameters: []modelParameterOutput{{
							ID:          "fast",
							DisplayName: "Fast",
							Values: []modelParamValueOutput{
								{Value: "false"},
								{Value: "true", DisplayName: "Fast"},
							},
						}},
						Variants: []modelVariantOutput{
							{Params: []modelParamOutput{{ID: "fast", Value: "true"}}, DisplayName: "Composer 2", IsDefault: true},
							{Params: []modelParamOutput{{ID: "fast", Value: "false"}}, DisplayName: "Composer 2"},
						},
					},
					{
						ID:          "claude-4.6-sonnet-thinking",
						DisplayName: "Claude 4.6 Sonnet (Thinking)",
						Parameters:  []modelParameterOutput{},
						Variants: []modelVariantOutput{
							{Params: []modelParamOutput{}, DisplayName: "Claude 4.6 Sonnet (Thinking)", IsDefault: true},
						},
					},
				}}, output)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}

func TestMCPCreateAgentSendsEveryRepository(t *testing.T) {
	var got cursorapi.CreateAgentRequest
	p := newTestPlugin(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/agents", r.URL.Path)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &got))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{
			"agent":{"id":"bc-created","name":"Created","status":"ACTIVE","url":"https://cursor.com/agents/bc-created"},
			"run":{"id":"run-created","status":"CREATING"}
		}`)
	}))
	autoCreatePR := true
	result, output, err := p.mcpCreateAgent(context.Background(), nil, createAgentInput{
		Prompt: "update the API and the web app",
		Repositories: []repositoryInput{
			{URL: " https://github.com/acme/api ", Ref: " main "},
			{URL: "https://github.com/acme/web"},
		},
		Model:        "composer-2",
		AutoCreatePR: &autoCreatePR,
	})
	require.NoError(t, err)
	assert.Nil(t, result)
	assert.Equal(t, "bc-created", output.AgentID)
	assert.Equal(t, cursorapi.CreateAgentRequest{
		Prompt: cursorapi.Prompt{Text: "update the API and the web app"},
		Repos: []cursorapi.RepoConfig{
			{URL: "https://github.com/acme/api", StartingRef: "main"},
			{URL: "https://github.com/acme/web"},
		},
		Model:        &cursorapi.ModelRef{ID: "composer-2"},
		AutoCreatePR: &autoCreatePR,
	}, got)
}

func TestMCPCreateAgentAcceptsSingleRepository(t *testing.T) {
	var got cursorapi.CreateAgentRequest
	p := newTestPlugin(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &got))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{
			"agent":{"id":"bc-created","name":"Created","status":"ACTIVE","url":"https://cursor.com/agents/bc-created"},
			"run":{"id":"run-created","status":"CREATING"}
		}`)
	}))
	recs := attachAuditCapture(p)
	result, _, err := p.mcpCreateAgent(context.Background(), nil, createAgentInput{
		Prompt:     "make a change",
		Repository: " https://github.com/acme/repo ",
		Ref:        " main ",
	})
	require.NoError(t, err)
	assert.Nil(t, result)
	assert.Equal(t, []cursorapi.RepoConfig{{
		URL:         "https://github.com/acme/repo",
		StartingRef: "main",
	}}, got.Repos)
	rec := requireAudit(t, recs, auditEventMCPCreateAgent, "create_agent", model.AuditStatusSuccess)
	assert.Equal(t, "https://github.com/acme/repo", rec.EventData.Parameters["repository"])
	assert.Equal(t, "main", rec.EventData.Parameters["ref"])
	_, hasRepositories := rec.EventData.Parameters["repositories"]
	assert.False(t, hasRepositories)
}

func TestMCPCreateAgentUsesNamedEnvironment(t *testing.T) {
	var got cursorapi.CreateAgentRequest
	p := newTestPlugin(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &got))
		require.NotContains(t, string(body), `"repos"`)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{
			"agent":{"id":"bc-created","name":"Created","status":"ACTIVE","url":"https://cursor.com/agents/bc-created"},
			"run":{"id":"run-created","status":"CREATING"}
		}`)
	}))
	recs := attachAuditCapture(p)
	result, _, err := p.mcpCreateAgent(context.Background(), nil, createAgentInput{
		Prompt:      "update both services",
		Environment: " Web app ",
	})
	require.NoError(t, err)
	assert.Nil(t, result)
	assert.Equal(t, cursorapi.CreateAgentRequest{
		Prompt: cursorapi.Prompt{Text: "update both services"},
		Env:    &cursorapi.AgentEnv{Type: "cloud", Name: "Web app"},
	}, got)
	rec := requireAudit(t, recs, auditEventMCPCreateAgent, "create_agent", model.AuditStatusSuccess)
	assert.Equal(t, "Web app", rec.EventData.Parameters["environment"])
}

func TestMCPCreateAgentRejectsInvalidRepositories(t *testing.T) {
	p := newTestPlugin(t, http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("upstream should not be called for an invalid repository list")
	}))
	recs := attachAuditCapture(p)
	tooMany := make([]repositoryInput, maxAgentRepositories+1)
	for i := range tooMany {
		tooMany[i] = repositoryInput{URL: "https://github.com/acme/repo-" + strconv.Itoa(i)}
	}

	tests := []struct {
		name  string
		input createAgentInput
		want  string
	}{
		{
			name:  "none",
			input: createAgentInput{Prompt: "should not launch"},
			want:  "At least one repository is required",
		},
		{
			name: "blank url",
			input: createAgentInput{
				Prompt:       "should not launch",
				Repositories: []repositoryInput{{URL: "  "}},
			},
			want: "Each repository needs a URL",
		},
		{
			name: "duplicate",
			input: createAgentInput{
				Prompt: "should not launch",
				Repositories: []repositoryInput{
					{URL: "https://github.com/acme/api"},
					{URL: " https://github.com/acme/api "},
				},
			},
			want: "Repository URLs must be unique",
		},
		{
			name: "too many",
			input: createAgentInput{
				Prompt:       "should not launch",
				Repositories: tooMany,
			},
			want: "Cursor Cloud accepts at most 500 repositories",
		},
		{
			name: "both forms",
			input: createAgentInput{
				Prompt:       "should not launch",
				Repository:   "https://github.com/acme/api",
				Repositories: []repositoryInput{{URL: "https://github.com/acme/web"}},
			},
			want: "Pass repository or repositories, not both",
		},
		{
			name: "ref without repository",
			input: createAgentInput{
				Prompt: "should not launch",
				Ref:    "main",
			},
			want: "A repository URL is required when ref is set",
		},
		{
			name: "environment with repositories",
			input: createAgentInput{
				Prompt:       "should not launch",
				Environment:  "Web app",
				Repositories: []repositoryInput{{URL: "https://github.com/acme/web"}},
			},
			want: "Omit repositories when environment is set",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			*recs = (*recs)[:0]
			result, output, err := p.mcpCreateAgent(context.Background(), nil, test.input)
			require.NoError(t, err)
			assert.Equal(t, createAgentOutput{}, output)
			require.NotNil(t, result)
			assert.True(t, result.IsError)
			require.Len(t, result.Content, 1)
			text, ok := result.Content[0].(*mcp.TextContent)
			require.True(t, ok)
			assert.Equal(t, test.want, text.Text)
			rec := requireAudit(t, recs, auditEventMCPCreateAgent, "create_agent", model.AuditStatusFail)
			assert.Equal(t, test.want, rec.Error.Description)
		})
	}
}

func TestMCPListEnvironmentsSendsFilters(t *testing.T) {
	limit := 20
	p := newTestPlugin(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/v1/environments", r.URL.Path)
		assert.Equal(t, "web", r.URL.Query().Get("query"))
		assert.Equal(t, "team", r.URL.Query().Get("owner"))
		assert.Equal(t, "20", r.URL.Query().Get("limit"))
		assert.Equal(t, "MjA", r.URL.Query().Get("cursor"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"items":[]}`)
	}))
	result, output, err := p.mcpListEnvironments(context.Background(), nil, listEnvironmentsInput{
		Query:  " web ",
		Owner:  " team ",
		Limit:  &limit,
		Cursor: " MjA ",
	})
	require.NoError(t, err)
	assert.Nil(t, result)
	assert.Empty(t, output.Environments)
	assert.NotNil(t, output.Environments)
	assert.Empty(t, output.NextCursor)
}

func TestMCPListEnvironmentsRejectsInvalidFilters(t *testing.T) {
	p := newTestPlugin(t, http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("upstream should not be called for invalid environment filters")
	}))
	limit := 101
	tests := []struct {
		name  string
		input listEnvironmentsInput
		want  string
	}{
		{
			name:  "owner",
			input: listEnvironmentsInput{Owner: "org"},
			want:  "owner must be personal or team",
		},
		{
			name:  "limit",
			input: listEnvironmentsInput{Limit: &limit},
			want:  "limit must be between 1 and 100",
		},
		{
			name:  "query",
			input: listEnvironmentsInput{Query: strings.Repeat("a", 256)},
			want:  "query must be at most 255 characters",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, output, err := p.mcpListEnvironments(context.Background(), nil, test.input)
			require.NoError(t, err)
			assert.Equal(t, listEnvironmentsOutput{}, output)
			require.NotNil(t, result)
			assert.True(t, result.IsError)
			text, ok := result.Content[0].(*mcp.TextContent)
			require.True(t, ok)
			assert.Equal(t, test.want, text.Text)
		})
	}
}

func TestMCPToolHandlersMissingKey(t *testing.T) {
	p := newTestPlugin(t, http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("upstream should not be called without the service account API key")
	}))
	config := *p.getConfiguration()
	config.ServiceAccountAPIKey = ""
	p.setConfiguration(&config)
	ctx := context.Background()

	tests := []struct {
		name string
		call func() *mcp.CallToolResult
	}{
		{
			name: "create_agent",
			call: func() *mcp.CallToolResult {
				result, _, _ := p.mcpCreateAgent(ctx, nil, createAgentInput{})
				return result
			},
		},
		{
			name: "get_agent",
			call: func() *mcp.CallToolResult {
				result, _, _ := p.mcpGetAgent(ctx, nil, getAgentInput{})
				return result
			},
		},
		{
			name: "list_agents",
			call: func() *mcp.CallToolResult {
				result, _, _ := p.mcpListAgents(ctx, nil, listAgentsInput{})
				return result
			},
		},
		{
			name: "list_environments",
			call: func() *mcp.CallToolResult {
				result, _, _ := p.mcpListEnvironments(ctx, nil, listEnvironmentsInput{})
				return result
			},
		},
		{
			name: "add_followup",
			call: func() *mcp.CallToolResult {
				result, _, _ := p.mcpAddFollowup(ctx, nil, addFollowupInput{})
				return result
			},
		},
		{
			name: "get_agent_conversation",
			call: func() *mcp.CallToolResult {
				result, _, _ := p.mcpGetAgentConversation(ctx, nil, getAgentConversationInput{})
				return result
			},
		},
		{
			name: "list_models",
			call: func() *mcp.CallToolResult {
				result, _, _ := p.mcpListModels(ctx, nil, listModelsInput{})
				return result
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := test.call()
			require.NotNil(t, result)
			assert.True(t, result.IsError)
			require.Len(t, result.Content, 1)
			text, ok := result.Content[0].(*mcp.TextContent)
			require.True(t, ok)
			assert.Contains(
				t,
				text.Text,
				"A Mattermost administrator must configure the Cursor Service Account API Key",
			)
		})
	}
}

func TestMCPUsesRotatedServiceAccountKey(t *testing.T) {
	const rotatedKey = "rotated-service-account-key"
	p := newTestPlugin(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer "+rotatedKey, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"items":[]}`)
	}))

	_, initialIdentity, failure := p.mcpClient()
	require.Nil(t, failure)
	config := *p.getConfiguration()
	config.ServiceAccountAPIKey = rotatedKey
	p.setConfiguration(&config)

	_, rotatedIdentity, failure := p.mcpClient()
	require.Nil(t, failure)
	assert.NotEqual(t, initialIdentity, rotatedIdentity)

	result, output, err := p.mcpListAgents(context.Background(), nil, listAgentsInput{})
	require.NoError(t, err)
	assert.Nil(t, result)
	assert.Empty(t, output.Agents)
}

func TestMCPGetAgentFallsBackToAgentStatus(t *testing.T) {
	p := newTestPlugin(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/agents/bc-idle", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"bc-idle","name":"Idle","status":"ACTIVE","env":{"type":"cloud"},
			"url":"https://cursor.com/agents/bc-idle",
			"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z",
			"repos":[],"workOnCurrentBranch":false,"autoCreatePR":false
		}`)
	}))
	recs := attachAuditCapture(p)

	result, output, err := p.mcpGetAgent(
		context.Background(),
		nil,
		getAgentInput{AgentID: "bc-idle"},
	)
	require.NoError(t, err)
	assert.Nil(t, result)
	assert.Equal(t, "ACTIVE", output.Status)
	rec := requireAudit(t, recs, auditEventMCPGetAgent, "get_agent", model.AuditStatusSuccess)
	assert.Equal(t, "bc-idle", rec.EventData.Parameters["agent_id"])
}

func TestConversationTruncationKeepsNewestContent(t *testing.T) {
	messages := []cursorapi.Message{
		{Type: "old", Text: strings.Repeat("a", 10)},
		{Type: "new", Text: strings.Repeat("b", 10)},
	}
	output := truncateMessages(messages, 12)
	require.True(t, output.Truncated)
	require.Len(t, output.Messages, 2)
	assert.Equal(t, "aa", output.Messages[0].Text)
	assert.Equal(t, strings.Repeat("b", 10), output.Messages[1].Text)

	output = truncateMessages([]cursorapi.Message{{Type: "new", Text: "Aé日"}}, 4)
	require.Len(t, output.Messages, 1)
	assert.Equal(t, "日", output.Messages[0].Text)
	assert.True(t, utf8.ValidString(output.Messages[0].Text))
}

func attachAuditCapture(p *Plugin) *[]*model.AuditRecord {
	recs := make([]*model.AuditRecord, 0, 1)
	p.recordAudit = func(rec *model.AuditRecord) {
		if rec == nil {
			return
		}
		cp := *rec
		cp.EventData.Parameters = maps.Clone(rec.EventData.Parameters)
		cp.Meta = maps.Clone(rec.Meta)
		recs = append(recs, &cp)
	}
	return &recs
}

func requireAudit(t *testing.T, recs *[]*model.AuditRecord, event, tool, status string) *model.AuditRecord {
	t.Helper()
	require.Len(t, *recs, 1)
	rec := (*recs)[0]
	assert.Equal(t, event, rec.EventName)
	assert.Equal(t, status, rec.Status)
	assert.Equal(t, tool, rec.EventData.Parameters["tool"])
	assert.Equal(t, auditMCPObjectType, rec.EventData.ObjectType)
	for _, key := range []string{"prompt", "text", "messages", "api_key", "ServiceAccountAPIKey"} {
		_, exists := rec.EventData.Parameters[key]
		assert.False(t, exists, "audit must not include %s", key)
	}
	return rec
}

func TestMCPToolAuditLogs(t *testing.T) {
	p := newTestPlugin(t, happyPathCursorAPI(t))
	recs := attachAuditCapture(p)
	ctx := context.Background()

	t.Run("create_agent", func(t *testing.T) {
		*recs = (*recs)[:0]
		result, _, err := p.mcpCreateAgent(ctx, nil, createAgentInput{
			Prompt: "make a change with secrets",
			Repositories: []repositoryInput{{
				URL: "https://github.com/acme/repo",
				Ref: "main",
			}},
			Model:       "composer-2",
			ModelParams: []modelParamInput{{ID: "fast", Value: "true"}},
		})
		require.NoError(t, err)
		assert.Nil(t, result)
		rec := requireAudit(t, recs, auditEventMCPCreateAgent, "create_agent", model.AuditStatusSuccess)
		assert.Equal(t, testMCPUserID, rec.Actor.UserId)
		assert.Equal(t, testMCPUserID, rec.EventData.Parameters["user_id"])
		assert.Equal(t, []string{"https://github.com/acme/repo"}, rec.EventData.Parameters["repositories"])
		assert.Equal(t, map[string]string{"https://github.com/acme/repo": "main"}, rec.EventData.Parameters["refs"])
		assert.Equal(t, "composer-2", rec.EventData.Parameters["model"])
		assert.Equal(t, map[string]string{"fast": "true"}, rec.EventData.Parameters["model_params"])
		assert.Equal(t, "bc-created", rec.EventData.Parameters["agent_id"])
	})

	t.Run("get_agent", func(t *testing.T) {
		*recs = (*recs)[:0]
		result, _, err := p.mcpGetAgent(ctx, nil, getAgentInput{AgentID: "bc-get"})
		require.NoError(t, err)
		assert.Nil(t, result)
		rec := requireAudit(t, recs, auditEventMCPGetAgent, "get_agent", model.AuditStatusSuccess)
		assert.Equal(t, testMCPUserID, rec.Actor.UserId)
		assert.Equal(t, "bc-get", rec.EventData.Parameters["agent_id"])
		_, hasSummary := rec.EventData.Parameters["summary"]
		assert.False(t, hasSummary)
	})

	t.Run("list_agents", func(t *testing.T) {
		*recs = (*recs)[:0]
		limit := 5
		result, _, err := p.mcpListAgents(ctx, nil, listAgentsInput{Limit: &limit})
		require.NoError(t, err)
		assert.Nil(t, result)
		rec := requireAudit(t, recs, auditEventMCPListAgents, "list_agents", model.AuditStatusSuccess)
		assert.Equal(t, testMCPUserID, rec.Actor.UserId)
		assert.Equal(t, 5, rec.EventData.Parameters["limit"])
	})

	t.Run("list_environments", func(t *testing.T) {
		*recs = (*recs)[:0]
		limit := 20
		result, _, err := p.mcpListEnvironments(ctx, nil, listEnvironmentsInput{
			Query:  " web ",
			Owner:  "team",
			Limit:  &limit,
			Cursor: "MjA",
		})
		require.NoError(t, err)
		assert.Nil(t, result)
		rec := requireAudit(t, recs, auditEventMCPListEnvironments, "list_environments", model.AuditStatusSuccess)
		assert.Equal(t, testMCPUserID, rec.Actor.UserId)
		assert.Equal(t, "web", rec.EventData.Parameters["query"])
		assert.Equal(t, "team", rec.EventData.Parameters["owner"])
		assert.Equal(t, 20, rec.EventData.Parameters["limit"])
		assert.Equal(t, "MjA", rec.EventData.Parameters["cursor"])
	})

	t.Run("add_followup", func(t *testing.T) {
		*recs = (*recs)[:0]
		result, _, err := p.mcpAddFollowup(ctx, nil, addFollowupInput{
			AgentID: "bc-follow",
			Prompt:  "also update tests and paste a token",
		})
		require.NoError(t, err)
		assert.Nil(t, result)
		rec := requireAudit(t, recs, auditEventMCPAddFollowup, "add_followup", model.AuditStatusSuccess)
		assert.Equal(t, testMCPUserID, rec.Actor.UserId)
		assert.Equal(t, "bc-follow", rec.EventData.Parameters["agent_id"])
	})

	t.Run("get_agent_conversation", func(t *testing.T) {
		*recs = (*recs)[:0]
		result, _, err := p.mcpGetAgentConversation(
			ctx,
			nil,
			getAgentConversationInput{AgentID: "bc-conversation"},
		)
		require.NoError(t, err)
		assert.Nil(t, result)
		rec := requireAudit(t, recs, auditEventMCPGetAgentConversation, "get_agent_conversation", model.AuditStatusSuccess)
		assert.Equal(t, testMCPUserID, rec.Actor.UserId)
		assert.Equal(t, "bc-conversation", rec.EventData.Parameters["agent_id"])
	})
}

func TestMCPToolAuditMissingUserID(t *testing.T) {
	p := newTestPlugin(t, http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("upstream should not be called without an acting user")
	}))
	p.getMCPUserID = func(context.Context) string { return "" }
	recs := attachAuditCapture(p)
	ctx := context.Background()

	tests := []struct {
		name  string
		event string
		tool  string
		call  func() *mcp.CallToolResult
	}{
		{
			name:  "create_agent",
			event: auditEventMCPCreateAgent,
			tool:  "create_agent",
			call: func() *mcp.CallToolResult {
				result, _, _ := p.mcpCreateAgent(ctx, nil, createAgentInput{
					Prompt: "should not launch",
					Repositories: []repositoryInput{{
						URL: "https://github.com/acme/repo",
					}},
				})
				return result
			},
		},
		{
			name:  "get_agent",
			event: auditEventMCPGetAgent,
			tool:  "get_agent",
			call: func() *mcp.CallToolResult {
				result, _, _ := p.mcpGetAgent(ctx, nil, getAgentInput{AgentID: "bc-get"})
				return result
			},
		},
		{
			name:  "list_agents",
			event: auditEventMCPListAgents,
			tool:  "list_agents",
			call: func() *mcp.CallToolResult {
				result, _, _ := p.mcpListAgents(ctx, nil, listAgentsInput{})
				return result
			},
		},
		{
			name:  "list_environments",
			event: auditEventMCPListEnvironments,
			tool:  "list_environments",
			call: func() *mcp.CallToolResult {
				result, _, _ := p.mcpListEnvironments(ctx, nil, listEnvironmentsInput{})
				return result
			},
		},
		{
			name:  "add_followup",
			event: auditEventMCPAddFollowup,
			tool:  "add_followup",
			call: func() *mcp.CallToolResult {
				result, _, _ := p.mcpAddFollowup(ctx, nil, addFollowupInput{
					AgentID: "bc-follow",
					Prompt:  "should not send",
				})
				return result
			},
		},
		{
			name:  "get_agent_conversation",
			event: auditEventMCPGetAgentConversation,
			tool:  "get_agent_conversation",
			call: func() *mcp.CallToolResult {
				result, _, _ := p.mcpGetAgentConversation(
					ctx,
					nil,
					getAgentConversationInput{AgentID: "bc-conversation"},
				)
				return result
			},
		},
		{
			name:  "list_models",
			event: auditEventMCPListModels,
			tool:  "list_models",
			call: func() *mcp.CallToolResult {
				result, _, _ := p.mcpListModels(ctx, nil, listModelsInput{})
				return result
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			*recs = (*recs)[:0]
			result := test.call()
			require.NotNil(t, result)
			assert.True(t, result.IsError)
			require.Len(t, result.Content, 1)
			text, ok := result.Content[0].(*mcp.TextContent)
			require.True(t, ok)
			assert.Equal(t, missingMCPUserMessage, text.Text)
			rec := requireAudit(t, recs, test.event, test.tool, model.AuditStatusFail)
			assert.Empty(t, rec.Actor.UserId)
			assert.Equal(t, "missing acting user ID", rec.Error.Description)
		})
	}
}

func TestMCPToolAuditMissingKeyStillRecordsActor(t *testing.T) {
	p := newTestPlugin(t, http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("upstream should not be called without the service account API key")
	}))
	config := *p.getConfiguration()
	config.ServiceAccountAPIKey = ""
	p.setConfiguration(&config)
	recs := attachAuditCapture(p)

	result, _, err := p.mcpGetAgent(context.Background(), nil, getAgentInput{AgentID: "bc-get"})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	rec := requireAudit(t, recs, auditEventMCPGetAgent, "get_agent", model.AuditStatusFail)
	assert.Equal(t, testMCPUserID, rec.Actor.UserId)
	assert.Equal(t, "bc-get", rec.EventData.Parameters["agent_id"])
	assert.Equal(t, "service account API key is not configured", rec.Error.Description)
}
