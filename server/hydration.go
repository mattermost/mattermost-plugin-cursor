package main

import (
	"context"
	"sync"

	"github.com/mattermost/mattermost-plugin-cursor/server/cursorapi"
)

const (
	hydrationConcurrency   = 5
	hydrationCacheCapacity = 1000
)

type agentCacheEntry struct {
	agent      cursorapi.Agent
	lastAccess uint64
}

type runCacheEntry struct {
	run        cursorapi.Run
	updatedAt  string
	runID      string
	lastAccess uint64
}

type hydrationKey struct {
	userID  string
	agentID string
}

type hydrationCache struct {
	mu       sync.Mutex
	capacity int
	clock    uint64
	agents   map[hydrationKey]agentCacheEntry
	runs     map[hydrationKey]runCacheEntry
}

func (c *hydrationCache) initialize(capacity int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.capacity = capacity
	c.agents = make(map[hydrationKey]agentCacheEntry)
	c.runs = make(map[hydrationKey]runCacheEntry)
}

func (c *hydrationCache) getAgent(userID, agentID string) (cursorapi.Agent, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := hydrationKey{userID: userID, agentID: agentID}
	entry, ok := c.agents[key]
	if !ok {
		return cursorapi.Agent{}, false
	}
	c.clock++
	entry.lastAccess = c.clock
	c.agents[key] = entry
	return entry.agent, true
}

func (c *hydrationCache) putAgent(userID string, agent cursorapi.Agent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := hydrationKey{userID: userID, agentID: agent.ID}
	if _, exists := c.agents[key]; !exists && len(c.agents) >= c.capacity {
		evictLeastRecentlyUsed(c.agents, func(entry agentCacheEntry) uint64 {
			return entry.lastAccess
		})
	}
	c.clock++
	c.agents[key] = agentCacheEntry{agent: agent, lastAccess: c.clock}
}

func (c *hydrationCache) getRun(
	userID string,
	agent cursorapi.AgentSummary,
) (cursorapi.Run, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := hydrationKey{userID: userID, agentID: agent.ID}
	entry, ok := c.runs[key]
	if !ok || entry.updatedAt != agent.UpdatedAt || entry.runID != agent.LatestRunID {
		return cursorapi.Run{}, false
	}
	c.clock++
	entry.lastAccess = c.clock
	c.runs[key] = entry
	return entry.run, true
}

func (c *hydrationCache) putRun(userID string, agent cursorapi.AgentSummary, run cursorapi.Run) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := hydrationKey{userID: userID, agentID: agent.ID}
	if _, exists := c.runs[key]; !exists && len(c.runs) >= c.capacity {
		evictLeastRecentlyUsed(c.runs, func(entry runCacheEntry) uint64 {
			return entry.lastAccess
		})
	}
	c.clock++
	c.runs[key] = runCacheEntry{
		run:        run,
		updatedAt:  agent.UpdatedAt,
		runID:      agent.LatestRunID,
		lastAccess: c.clock,
	}
}

func evictLeastRecentlyUsed[K comparable, T any](entries map[K]T, access func(T) uint64) {
	var oldestKey K
	var oldestAccess uint64
	first := true
	for key, value := range entries {
		lastAccess := access(value)
		if first || lastAccess < oldestAccess {
			oldestKey = key
			oldestAccess = lastAccess
			first = false
		}
	}
	delete(entries, oldestKey)
}

func (p *Plugin) hydrateAgents(
	ctx context.Context,
	userID string,
	client *cursorapi.Client,
	agents []cursorapi.AgentSummary,
	includeRun bool,
) []cursorapi.HydratedAgent {
	hydrated := make([]cursorapi.HydratedAgent, len(agents))
	for index, agent := range agents {
		hydrated[index] = cursorapi.HydratedAgent{AgentSummary: agent}
	}
	jobs := make(chan int)
	workers := min(hydrationConcurrency, len(agents))
	var waitGroup sync.WaitGroup
	waitGroup.Add(workers)
	for range workers {
		go func() {
			defer waitGroup.Done()
			for index := range jobs {
				hydrated[index], _ = p.hydrateAgent(ctx, userID, client, agents[index], includeRun)
			}
		}()
	}
dispatch:
	for index := range agents {
		select {
		case jobs <- index:
		case <-ctx.Done():
			break dispatch
		}
	}
	close(jobs)
	waitGroup.Wait()
	return hydrated
}

func (p *Plugin) hydrateAgent(
	ctx context.Context,
	userID string,
	client *cursorapi.Client,
	summary cursorapi.AgentSummary,
	includeRun bool,
) (cursorapi.HydratedAgent, bool) {
	hydrated := cursorapi.HydratedAgent{AgentSummary: summary}
	agent, ok := p.hydration.getAgent(userID, summary.ID)
	if !ok {
		response, err := client.GetAgent(ctx, summary.ID)
		if err != nil {
			p.logDebug("Failed to fetch agent during hydration", "agent_id", summary.ID, "error", err.Error())
		} else if agent, err = cursorapi.Decode[cursorapi.Agent](response); err != nil {
			p.logDebug("Failed to decode agent during hydration", "agent_id", summary.ID, "error", err.Error())
		} else {
			p.hydration.putAgent(userID, agent)
			ok = true
		}
	}
	if ok {
		hydrated.Repos = agent.Repos
	}
	if !includeRun || summary.LatestRunID == "" {
		return hydrated, false
	}

	run, ok := p.hydration.getRun(userID, summary)
	if !ok {
		response, err := client.GetRun(ctx, summary.ID, summary.LatestRunID)
		if err != nil {
			p.logDebug(
				"Failed to fetch run during hydration",
				"agent_id", summary.ID,
				"run_id", summary.LatestRunID,
				"error", err.Error(),
			)
			return hydrated, false
		}
		run, err = cursorapi.Decode[cursorapi.Run](response)
		if err != nil {
			p.logDebug(
				"Failed to decode run during hydration",
				"agent_id", summary.ID,
				"run_id", summary.LatestRunID,
				"error", err.Error(),
			)
			return hydrated, false
		}
		p.hydration.putRun(userID, summary, run)
	}
	if len(run.Git.Branches) > 0 {
		hydrated.Branch = run.Git.Branches[0].Branch
		hydrated.PRURL = run.Git.Branches[0].PRURL
	}
	hydrated.RunStatus = run.Status
	hydrated.Result = run.Result
	return hydrated, true
}
