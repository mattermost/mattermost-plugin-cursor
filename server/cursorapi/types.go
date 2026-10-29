package cursorapi

type Prompt struct {
	Text string `json:"text"`
}

// ModelParam is a single per-model parameter applied to a run, matching the
// shape Cursor expects in model.params (see GET /v1/models for valid values).
type ModelParam struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}

// ModelRef is the model object sent on Create An Agent. ID is required whenever
// a model is provided; Params is optional.
type ModelRef struct {
	ID     string       `json:"id"`
	Params []ModelParam `json:"params,omitempty"`
}

type RepoConfig struct {
	URL         string `json:"url"`
	StartingRef string `json:"startingRef,omitempty"`
}

type CreateAgentRequest struct {
	Prompt       Prompt       `json:"prompt"`
	Env          *AgentEnv    `json:"env,omitempty"`
	Repos        []RepoConfig `json:"repos,omitempty"`
	Model        *ModelRef    `json:"model,omitempty"`
	AutoCreatePR *bool        `json:"autoCreatePR,omitempty"`
}

func NewCreateAgentRequest(
	prompt string,
	repos []RepoConfig,
	environment string,
	model *ModelRef,
	autoCreatePR *bool,
) CreateAgentRequest {
	request := CreateAgentRequest{
		Prompt:       Prompt{Text: prompt},
		Repos:        repos,
		Model:        model,
		AutoCreatePR: autoCreatePR,
	}
	if environment != "" {
		request.Env = &AgentEnv{Type: "cloud", Name: environment}
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

type Environment struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Owner     string       `json:"owner"`
	Repos     []RepoConfig `json:"repos,omitempty"`
	CreatedAt string       `json:"createdAt"`
	UpdatedAt string       `json:"updatedAt"`
}

type ListEnvironmentsResponse struct {
	Items      []Environment `json:"items"`
	NextCursor string        `json:"nextCursor,omitempty"`
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

// ModelParamValue is one permitted value for a model parameter.
type ModelParamValue struct {
	Value       string `json:"value"`
	DisplayName string `json:"displayName,omitempty"`
}

// ModelParameter describes one tunable parameter for a model and its permitted
// values, as returned by GET /v1/models.
type ModelParameter struct {
	ID          string            `json:"id"`
	DisplayName string            `json:"displayName,omitempty"`
	Values      []ModelParamValue `json:"values,omitempty"`
}

// ModelVariant is a concrete id+params combination a model accepts.
type ModelVariant struct {
	Params      []ModelParam `json:"params,omitempty"`
	DisplayName string       `json:"displayName,omitempty"`
	Description string       `json:"description,omitempty"`
	IsDefault   bool         `json:"isDefault,omitempty"`
}

// Model describes a single recommended model from GET /v1/models.
type Model struct {
	ID          string           `json:"id"`
	DisplayName string           `json:"displayName,omitempty"`
	Description string           `json:"description,omitempty"`
	Aliases     []string         `json:"aliases,omitempty"`
	Parameters  []ModelParameter `json:"parameters,omitempty"`
	Variants    []ModelVariant   `json:"variants,omitempty"`
}

type ListModelsResponse struct {
	Items []Model `json:"items"`
}
