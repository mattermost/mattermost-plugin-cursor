package cursorapi

type Prompt struct {
	Text string `json:"text"`
}

type ModelRef struct {
	ID string `json:"id"`
}

type RepoConfig struct {
	URL         string `json:"url"`
	StartingRef string `json:"startingRef,omitempty"`
}

type CreateAgentRequest struct {
	Prompt       Prompt       `json:"prompt"`
	Repos        []RepoConfig `json:"repos,omitempty"`
	Model        *ModelRef    `json:"model,omitempty"`
	AutoCreatePR *bool        `json:"autoCreatePR,omitempty"`
}

func NewCreateAgentRequest(prompt string, repos []RepoConfig, model string, autoCreatePR *bool) CreateAgentRequest {
	request := CreateAgentRequest{
		Prompt:       Prompt{Text: prompt},
		Repos:        repos,
		AutoCreatePR: autoCreatePR,
	}
	if model != "" {
		request.Model = &ModelRef{ID: model}
	}
	return request
}

type CreateRunRequest struct {
	Prompt Prompt `json:"prompt"`
}

type APIKeyInfo struct {
	UserEmail string `json:"userEmail"`
}

type AgentEnv struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

type AgentSummary struct {
	ID          string   `json:"id"`
	Name        string   `json:"name,omitempty"`
	Status      string   `json:"status"`
	Env         AgentEnv `json:"env"`
	URL         string   `json:"url"`
	CreatedAt   string   `json:"createdAt"`
	UpdatedAt   string   `json:"updatedAt"`
	LatestRunID string   `json:"latestRunId,omitempty"`
}

type Agent struct {
	AgentSummary
	Repos               []RepoConfig `json:"repos,omitempty"`
	WorkOnCurrentBranch bool         `json:"workOnCurrentBranch"`
	AutoCreatePR        bool         `json:"autoCreatePR"`
}

func (a Agent) RepositoryURL() string {
	if len(a.Repos) == 0 {
		return ""
	}
	return a.Repos[0].URL
}

type GitBranch struct {
	Branch string `json:"branch"`
	PRURL  string `json:"prUrl"`
}

type RunGit struct {
	Branches []GitBranch `json:"branches"`
}

type Run struct {
	ID        string `json:"id"`
	AgentID   string `json:"agentId"`
	Status    string `json:"status"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	Result    string `json:"result,omitempty"`
	Git       RunGit `json:"git"`
}

type CreateAgentResponse struct {
	Agent Agent `json:"agent"`
	Run   Run   `json:"run"`
}

type CreateRunResponse struct {
	Run Run `json:"run"`
}

type ListAgentsResponse struct {
	Items      []AgentSummary `json:"items"`
	NextCursor string         `json:"nextCursor,omitempty"`
}

type Message struct {
	ID   string `json:"id,omitempty"`
	Type string `json:"type"`
	Text string `json:"text"`
}

type ConversationResponse struct {
	ID       string    `json:"id"`
	Messages []Message `json:"messages"`
}

type HydratedAgent struct {
	AgentSummary
	Repos     []RepoConfig `json:"repos,omitempty"`
	Branch    string       `json:"branch,omitempty"`
	PRURL     string       `json:"prUrl,omitempty"`
	RunStatus string       `json:"runStatus"`
	Result    string       `json:"-"`
}

type HydratedListAgentsResponse struct {
	Items      []HydratedAgent `json:"items"`
	NextCursor string          `json:"nextCursor,omitempty"`
}
