// Package store keeps the sites, votes and voting state.
//
// Two implementations: Memory (for `go run ./cmd/dev`) and Upstash
// (Redis over REST, used on Vercel). New picks the right one from the
// environment.
package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"
)

// Site is one student submission.
type Site struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Tagline string `json:"tagline"`
	Owner   string `json:"owner"` // browser id of whoever submitted it
	Created int64  `json:"created"`
}

// State is the host-controlled voting state.
type State struct {
	Locked bool `json:"locked"`
}

// Store is everything the handlers need.
type Store interface {
	AddSite(s Site) error
	Sites() ([]Site, error)
	DeleteSite(id string) error

	AddVote(siteID, voter string) error
	RemoveVote(siteID, voter string) error
	Picks(voter string) ([]string, error)
	Counts() (map[string]int, error)
	VoterCount() (int, error)
	ResetVotes() error

	State() (State, error)
	SetState(State) error
}

// New returns an Upstash store when its env vars are set, otherwise Memory.
func New() Store {
	url := firstEnv("KV_REST_API_URL", "UPSTASH_REDIS_REST_URL")
	token := firstEnv("KV_REST_API_TOKEN", "UPSTASH_REDIS_REST_TOKEN")
	if url != "" && token != "" {
		return &Upstash{URL: url, Token: token, Client: &http.Client{Timeout: 8 * time.Second}}
	}
	return sharedMemory
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// ---------------------------------------------------------------- memory

var sharedMemory = NewMemory()

// Memory is an in-process store. Only for local development: Vercel
// functions don't share memory, so votes would scatter across instances.
type Memory struct {
	mu    sync.Mutex
	sites map[string]Site
	votes map[string]map[string]bool // siteID -> voter -> true
	picks map[string]map[string]bool // voter -> siteID -> true
	state State
}

func NewMemory() *Memory {
	return &Memory{sites: map[string]Site{}, votes: map[string]map[string]bool{}, picks: map[string]map[string]bool{}}
}

func (m *Memory) AddSite(s Site) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sites[s.ID] = s
	return nil
}

func (m *Memory) Sites() ([]Site, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Site, 0, len(m.sites))
	for _, s := range m.sites {
		out = append(out, s)
	}
	sortSites(out)
	return out, nil
}

func (m *Memory) DeleteSite(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sites, id)
	delete(m.votes, id)
	for _, p := range m.picks {
		delete(p, id)
	}
	return nil
}

func (m *Memory) AddVote(siteID, voter string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.votes[siteID] == nil {
		m.votes[siteID] = map[string]bool{}
	}
	if m.picks[voter] == nil {
		m.picks[voter] = map[string]bool{}
	}
	m.votes[siteID][voter] = true
	m.picks[voter][siteID] = true
	return nil
}

func (m *Memory) RemoveVote(siteID, voter string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.votes[siteID], voter)
	delete(m.picks[voter], siteID)
	return nil
}

func (m *Memory) Picks(voter string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []string{}
	for id := range m.picks[voter] {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

func (m *Memory) Counts() (map[string]int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]int{}
	for id, vs := range m.votes {
		out[id] = len(vs)
	}
	return out, nil
}

func (m *Memory) VoterCount() (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, p := range m.picks {
		if len(p) > 0 {
			n++
		}
	}
	return n, nil
}

func (m *Memory) ResetVotes() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.votes = map[string]map[string]bool{}
	m.picks = map[string]map[string]bool{}
	return nil
}

func (m *Memory) State() (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state, nil
}

func (m *Memory) SetState(s State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = s
	return nil
}

// ---------------------------------------------------------------- upstash

// Upstash talks to Upstash Redis over its REST API.
//
// Keys:
//
//	sites            hash  id -> site JSON
//	votes:<siteID>   set   voter ids
//	picks:<voter>    set   site ids
//	voters           set   every voter id that ever voted (for reset)
//	state            hash  locked -> "1"/"0"
type Upstash struct {
	URL    string
	Token  string
	Client *http.Client
}

func (u *Upstash) cmd(args ...any) (json.RawMessage, error) {
	body, _ := json.Marshal(args)
	req, err := http.NewRequest("POST", u.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+u.Token)
	req.Header.Set("Content-Type", "application/json")
	res, err := u.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out struct {
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("upstash: bad response %q", string(raw))
	}
	if out.Error != "" {
		return nil, errors.New("upstash: " + out.Error)
	}
	return out.Result, nil
}

func (u *Upstash) pipeline(cmds [][]any) ([]json.RawMessage, error) {
	if len(cmds) == 0 {
		return nil, nil
	}
	body, _ := json.Marshal(cmds)
	req, err := http.NewRequest("POST", u.URL+"/pipeline", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+u.Token)
	req.Header.Set("Content-Type", "application/json")
	res, err := u.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out []struct {
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("upstash: bad pipeline response %q", string(raw))
	}
	results := make([]json.RawMessage, len(out))
	for i, o := range out {
		if o.Error != "" {
			return nil, errors.New("upstash: " + o.Error)
		}
		results[i] = o.Result
	}
	return results, nil
}

func (u *Upstash) AddSite(s Site) error {
	b, _ := json.Marshal(s)
	_, err := u.cmd("HSET", "sites", s.ID, string(b))
	return err
}

func (u *Upstash) Sites() ([]Site, error) {
	raw, err := u.cmd("HGETALL", "sites")
	if err != nil {
		return nil, err
	}
	var flat []string
	if err := json.Unmarshal(raw, &flat); err != nil {
		return nil, err
	}
	out := []Site{}
	for i := 1; i < len(flat); i += 2 {
		var s Site
		if json.Unmarshal([]byte(flat[i]), &s) == nil {
			out = append(out, s)
		}
	}
	sortSites(out)
	return out, nil
}

func (u *Upstash) DeleteSite(id string) error {
	_, err := u.pipeline([][]any{{"HDEL", "sites", id}, {"DEL", "votes:" + id}})
	return err
}

func (u *Upstash) AddVote(siteID, voter string) error {
	_, err := u.pipeline([][]any{
		{"SADD", "votes:" + siteID, voter},
		{"SADD", "picks:" + voter, siteID},
		{"SADD", "voters", voter},
	})
	return err
}

func (u *Upstash) RemoveVote(siteID, voter string) error {
	_, err := u.pipeline([][]any{
		{"SREM", "votes:" + siteID, voter},
		{"SREM", "picks:" + voter, siteID},
	})
	return err
}

func (u *Upstash) Picks(voter string) ([]string, error) {
	raw, err := u.cmd("SMEMBERS", "picks:"+voter)
	if err != nil {
		return nil, err
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

func (u *Upstash) Counts() (map[string]int, error) {
	sites, err := u.Sites()
	if err != nil {
		return nil, err
	}
	cmds := make([][]any, 0, len(sites))
	for _, s := range sites {
		cmds = append(cmds, []any{"SCARD", "votes:" + s.ID})
	}
	res, err := u.pipeline(cmds)
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	for i, s := range sites {
		var n int
		json.Unmarshal(res[i], &n)
		out[s.ID] = n
	}
	return out, nil
}

func (u *Upstash) VoterCount() (int, error) {
	raw, err := u.cmd("SCARD", "voters")
	if err != nil {
		return 0, err
	}
	var n int
	json.Unmarshal(raw, &n)
	return n, nil
}

func (u *Upstash) ResetVotes() error {
	sites, err := u.Sites()
	if err != nil {
		return err
	}
	raw, err := u.cmd("SMEMBERS", "voters")
	if err != nil {
		return err
	}
	var voters []string
	json.Unmarshal(raw, &voters)
	keys := []any{"DEL", "voters"}
	for _, s := range sites {
		keys = append(keys, "votes:"+s.ID)
	}
	for _, v := range voters {
		keys = append(keys, "picks:"+v)
	}
	_, err = u.cmd(keys...)
	return err
}

func (u *Upstash) State() (State, error) {
	raw, err := u.cmd("HGET", "state", "locked")
	if err != nil {
		return State{}, err
	}
	var v string
	json.Unmarshal(raw, &v)
	return State{Locked: v == "1"}, nil
}

func (u *Upstash) SetState(s State) error {
	v := "0"
	if s.Locked {
		v = "1"
	}
	_, err := u.cmd("HSET", "state", "locked", v)
	return err
}

func sortSites(s []Site) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].Created != s[j].Created {
			return s[i].Created < s[j].Created
		}
		return s[i].ID < s[j].ID
	})
}
