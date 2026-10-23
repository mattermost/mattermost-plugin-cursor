package cursorapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const DefaultBaseURL = "https://api.cursor.com"

type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

func (r Response) Successful() bool {
	return r.StatusCode >= http.StatusOK && r.StatusCode < http.StatusMultipleChoices
}

type StreamResponse struct {
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser
}

func (r StreamResponse) Successful() bool {
	return r.StatusCode >= http.StatusOK && r.StatusCode < http.StatusMultipleChoices
}

type APIError struct {
	StatusCode int
	Body       []byte
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Cursor API returned status %d", e.StatusCode)
}

func Decode[T any](response Response) (T, error) {
	var value T
	if !response.Successful() {
		return value, &APIError{StatusCode: response.StatusCode, Body: response.Body}
	}
	if err := json.Unmarshal(response.Body, &value); err != nil {
		return value, fmt.Errorf("decode Cursor API response: %w", err)
	}
	return value, nil
}

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func NewClient(baseURL, apiKey string, httpClient *http.Client) *Client {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultBaseURL
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: httpClient,
	}
}

func (c *Client) GetMe(ctx context.Context) (Response, error) {
	return c.do(ctx, http.MethodGet, "/v1/me", nil, nil)
}

func (c *Client) ListAgents(ctx context.Context, query url.Values) (Response, error) {
	return c.do(ctx, http.MethodGet, "/v1/agents", query, nil)
}

func (c *Client) ListEnvironments(ctx context.Context, query url.Values) (Response, error) {
	return c.do(ctx, http.MethodGet, "/v1/environments", query, nil)
}

func (c *Client) CreateAgent(ctx context.Context, request CreateAgentRequest) (Response, error) {
	return c.do(ctx, http.MethodPost, "/v1/agents", nil, request)
}

func (c *Client) GetAgent(ctx context.Context, agentID string) (Response, error) {
	return c.do(ctx, http.MethodGet, agentPath(agentID), nil, nil)
}

func (c *Client) DeleteAgent(ctx context.Context, agentID string) (Response, error) {
	return c.do(ctx, http.MethodDelete, agentPath(agentID), nil, nil)
}

func (c *Client) ArchiveAgent(ctx context.Context, agentID string) (Response, error) {
	return c.do(ctx, http.MethodPost, agentPath(agentID)+"/archive", nil, nil)
}

func (c *Client) UnarchiveAgent(ctx context.Context, agentID string) (Response, error) {
	return c.do(ctx, http.MethodPost, agentPath(agentID)+"/unarchive", nil, nil)
}

func (c *Client) GetConversation(ctx context.Context, agentID string) (Response, error) {
	// v1 has no conversation reader yet, so use the supported v0 endpoint.
	return c.do(ctx, http.MethodGet, "/v0/agents/"+url.PathEscape(agentID)+"/conversation", nil, nil)
}

func (c *Client) CreateRun(ctx context.Context, agentID string, request CreateRunRequest) (Response, error) {
	return c.do(ctx, http.MethodPost, agentPath(agentID)+"/runs", nil, request)
}

func (c *Client) GetRun(ctx context.Context, agentID, runID string) (Response, error) {
	return c.do(ctx, http.MethodGet, runPath(agentID, runID), nil, nil)
}

func (c *Client) CancelRun(ctx context.Context, agentID, runID string) (Response, error) {
	return c.do(ctx, http.MethodPost, runPath(agentID, runID)+"/cancel", nil, nil)
}

func (c *Client) StreamRun(ctx context.Context, agentID, runID, lastEventID string) (*StreamResponse, error) {
	request, err := c.newRequest(ctx, http.MethodGet, runPath(agentID, runID)+"/stream", nil, nil)
	if err != nil {
		return nil, err
	}
	if lastEventID != "" {
		request.Header.Set("Last-Event-ID", lastEventID)
	}
	request.Header.Set("Accept", "text/event-stream")
	// The caller owns the response body for the lifetime of the stream.
	//nolint:bodyclose
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("call Cursor API: %w", err)
	}
	return &StreamResponse{
		StatusCode: response.StatusCode,
		Header:     response.Header.Clone(),
		Body:       response.Body,
	}, nil
}

func (c *Client) ListModels(ctx context.Context) (Response, error) {
	return c.do(ctx, http.MethodGet, "/v1/models", nil, nil)
}

func (c *Client) ListRepositories(ctx context.Context) (Response, error) {
	return c.do(ctx, http.MethodGet, "/v1/repositories", nil, nil)
}

func (c *Client) do(ctx context.Context, method, route string, query url.Values, body any) (Response, error) {
	request, err := c.newRequest(ctx, method, route, query, body)
	if err != nil {
		return Response{}, err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return Response{}, fmt.Errorf("call Cursor API: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return Response{}, fmt.Errorf("read Cursor API response: %w", err)
	}
	return Response{
		StatusCode: response.StatusCode,
		Header:     response.Header.Clone(),
		Body:       responseBody,
	}, nil
}

func (c *Client) newRequest(
	ctx context.Context,
	method string,
	route string,
	query url.Values,
	body any,
) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode Cursor API request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	endpoint, err := url.Parse(c.baseURL + route)
	if err != nil {
		return nil, fmt.Errorf("build Cursor API URL: %w", err)
	}
	if query != nil {
		endpoint.RawQuery = query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), reader)
	if err != nil {
		return nil, fmt.Errorf("build Cursor API request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	return request, nil
}

func agentPath(agentID string) string {
	return "/v1/agents/" + url.PathEscape(agentID)
}

func runPath(agentID, runID string) string {
	return agentPath(agentID) + "/runs/" + url.PathEscape(runID)
}
