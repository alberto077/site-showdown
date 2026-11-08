// Package app holds the HTTP handlers. The Vercel functions under api/
// and the local dev server both call into here.
package app

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"showdown/store"
)

const maxPicks = 3
const maxSites = 80

var db = store.New()

// hostPIN protects the host board, reveal data and admin actions.
func hostPIN() string {
	if p := os.Getenv("HOST_PIN"); p != "" {
		return p
	}
	return "kaboom"
}

func pinOK(r *http.Request) bool {
	got := r.Header.Get("X-Pin")
	if got == "" {
		got = r.URL.Query().Get("pin")
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(hostPIN())) == 1
}

func voter(r *http.Request) string {
	v := r.Header.Get("X-Voter")
	if len(v) > 64 {
		v = v[:64]
	}
	return v
}

func newID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// ---------------------------------------------------------------- views

type siteView struct {
	store.Site
	Host   string
	Mine   bool
	Picked bool
	Count  int
	Pct    int
	Rank   int
}

type listView struct {
	Msg    string
	Sites  []siteView
	Picks  int
	Max    int
	Locked bool
}

type boardView struct {
	Sites      []siteView // submission order, for the preview grid
	Ranked     []siteView // by votes
	TotalVotes int
	Voters     int
	Locked     bool
}

var tmpl = template.Must(template.New("").Parse(`
{{define "list"}}
<div id="list">
{{if .Msg}}<p class="flash" role="status">{{.Msg}}</p>{{end}}
<p class="picks" aria-live="polite">
  {{if .Locked}}<strong>Voting is closed.</strong> Eyes on the big screen.
  {{else}}Your picks: <strong>{{.Picks}} of {{.Max}}</strong>. Tap a star to pick a site, tap again to change your mind.{{end}}
</p>
{{if not .Sites}}<p class="empty">No sites yet. Yours could be first.</p>{{end}}
<ul class="sites">
{{range .Sites}}
  <li class="site{{if .Mine}} mine{{end}}{{if .Picked}} picked{{end}}" id="s-{{.ID}}">
    <div class="who">
      <strong>{{.Name}}</strong>
      {{if .Tagline}}<span class="tag">{{.Tagline}}</span>{{end}}
      <a href="{{.URL}}" target="_blank" rel="noopener">{{.Host}} ↗</a>
    </div>
    {{if .Mine}}
      <span class="badge">yours</span>
      <button type="button" class="ghost" hx-delete="/api/sites?id={{.ID}}" hx-target="#list" hx-swap="outerHTML" hx-confirm="Remove your site from the showdown?">Remove</button>
    {{else}}
      <button type="button" class="star{{if .Picked}} on{{end}}" hx-post="/api/vote" hx-vals='{"id":"{{.ID}}"}' hx-target="#list" hx-swap="outerHTML" {{if $.Locked}}disabled{{end}} aria-pressed="{{if .Picked}}true{{else}}false{{end}}" aria-label="Pick {{.Name}}">
        {{if .Picked}}★ Picked{{else}}☆ Pick{{end}}
      </button>
    {{end}}
  </li>
{{end}}
</ul>
</div>
{{end}}

{{define "board"}}
<div id="board">
<section class="standings">
  <div class="meta">
    <span class="pill {{if .Locked}}closed{{else}}open{{end}}">{{if .Locked}}Voting closed{{else}}Voting open{{end}}</span>
    <span>{{.TotalVotes}} votes from {{.Voters}} voters · {{len .Sites}} sites</span>
  </div>
  <ol class="rank">
  {{range .Ranked}}
    <li id="r-{{.ID}}">
      <span class="pos">{{.Rank}}</span>
      <span class="name">{{.Name}}</span>
      <span class="bar"><i style="width:{{.Pct}}%"></i></span>
      <span class="n">{{.Count}}</span>
    </li>
  {{end}}
  </ol>
  {{if not .Sites}}<p class="empty">Nothing submitted yet. Share the student link.</p>{{end}}
</section>
<section class="grid">
{{range .Sites}}
  <article class="card" id="p-{{.ID}}">
    <header>
      <strong>{{.Name}}</strong>
      <b class="count">{{.Count}}</b>
    </header>
    <div class="shot"><iframe id="f-{{.ID}}" src="{{.URL}}" title="{{.Name}}" loading="lazy" sandbox="allow-scripts allow-same-origin"></iframe></div>
    <footer>
      <a href="{{.URL}}" target="_blank" rel="noopener">{{.Host}} ↗</a>
      <button type="button" class="ghost" hx-post="/api/admin" hx-vals='{"action":"delete","id":"{{.ID}}"}' hx-target="#board" hx-swap="morph:outerHTML" hx-confirm="Remove {{.Name}}?">Remove</button>
    </footer>
  </article>
{{end}}
</section>
</div>
{{end}}
`))

func render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

func fail(w http.ResponseWriter, err error) {
	http.Error(w, "Storage error: "+err.Error(), 500)
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return u.Host
}

func buildList(w http.ResponseWriter, r *http.Request, msg string) {
	sites, err := db.Sites()
	if err != nil {
		fail(w, err)
		return
	}
	st, _ := db.State()
	v := voter(r)
	picks, _ := db.Picks(v)
	picked := map[string]bool{}
	for _, p := range picks {
		picked[p] = true
	}
	lv := listView{Msg: msg, Max: maxPicks, Locked: st.Locked}
	for _, s := range sites {
		sv := siteView{Site: s, Host: hostOf(s.URL), Mine: s.Owner == v && v != "", Picked: picked[s.ID]}
		if sv.Picked {
			lv.Picks++
		}
		lv.Sites = append(lv.Sites, sv)
	}
	render(w, "list", lv)
}

func buildBoard(w http.ResponseWriter, r *http.Request) {
	bv, err := board()
	if err != nil {
		fail(w, err)
		return
	}
	render(w, "board", bv)
}

func board() (boardView, error) {
	sites, err := db.Sites()
	if err != nil {
		return boardView{}, err
	}
	counts, err := db.Counts()
	if err != nil {
		return boardView{}, err
	}
	voters, _ := db.VoterCount()
	st, _ := db.State()
	bv := boardView{Voters: voters, Locked: st.Locked}
	top := 0
	for _, s := range sites {
		c := counts[s.ID]
		bv.TotalVotes += c
		if c > top {
			top = c
		}
		bv.Sites = append(bv.Sites, siteView{Site: s, Host: hostOf(s.URL), Count: c})
	}
	bv.Ranked = append([]siteView(nil), bv.Sites...)
	sort.SliceStable(bv.Ranked, func(i, j int) bool { return bv.Ranked[i].Count > bv.Ranked[j].Count })
	for i := range bv.Ranked {
		bv.Ranked[i].Rank = i + 1
		if top > 0 {
			bv.Ranked[i].Pct = bv.Ranked[i].Count * 100 / top
		}
	}
	return bv, nil
}

// ---------------------------------------------------------------- handlers

// Sites: GET lists, POST submits, DELETE removes your own.
func Sites(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		buildList(w, r, "")
	case http.MethodPost:
		submit(w, r)
	case http.MethodDelete:
		id := r.URL.Query().Get("id")
		sites, err := db.Sites()
		if err != nil {
			fail(w, err)
			return
		}
		for _, s := range sites {
			if s.ID == id && s.Owner == voter(r) && s.Owner != "" {
				if err := db.DeleteSite(id); err != nil {
					fail(w, err)
					return
				}
				buildList(w, r, "Your site was removed.")
				return
			}
		}
		buildList(w, r, "That site isn't yours to remove.")
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func submit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		buildList(w, r, "Couldn't read the form. Try again.")
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	raw := strings.TrimSpace(r.FormValue("url"))
	tagline := strings.TrimSpace(r.FormValue("tagline"))
	if name == "" || raw == "" {
		buildList(w, r, "Add your name and your site's address.")
		return
	}
	if len(name) > 40 {
		name = name[:40]
	}
	if len(tagline) > 80 {
		tagline = tagline[:80]
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || !strings.Contains(u.Host, ".") {
		buildList(w, r, "That address doesn't look right. It should look like yourname.github.io")
		return
	}
	u.Scheme = "https"
	u.Fragment = ""
	st, _ := db.State()
	if st.Locked {
		buildList(w, r, "Voting is closed, so submissions are too.")
		return
	}
	sites, err := db.Sites()
	if err != nil {
		fail(w, err)
		return
	}
	if len(sites) >= maxSites {
		buildList(w, r, "The showdown is full.")
		return
	}
	v := voter(r)
	for _, s := range sites {
		if strings.EqualFold(strings.TrimRight(s.URL, "/"), strings.TrimRight(u.String(), "/")) {
			buildList(w, r, "That site is already in. One entry per site.")
			return
		}
		if v != "" && s.Owner == v {
			buildList(w, r, "You already submitted a site. Remove it first to swap it.")
			return
		}
	}
	err = db.AddSite(store.Site{ID: newID(), Name: name, URL: u.String(), Tagline: tagline, Owner: v, Created: time.Now().UnixMilli()})
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("HX-Trigger", "submitted")
	buildList(w, r, "You're in! Now pick your favorites.")
}

// Vote toggles one pick for the calling voter.
func Vote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	r.ParseForm()
	id := r.FormValue("id")
	v := voter(r)
	if v == "" {
		buildList(w, r, "Your browser didn't send an id. Reload the page and try again.")
		return
	}
	st, _ := db.State()
	if st.Locked {
		buildList(w, r, "Voting is closed.")
		return
	}
	sites, err := db.Sites()
	if err != nil {
		fail(w, err)
		return
	}
	var target *store.Site
	for i := range sites {
		if sites[i].ID == id {
			target = &sites[i]
		}
	}
	if target == nil {
		buildList(w, r, "That site was removed.")
		return
	}
	if target.Owner == v {
		buildList(w, r, "Nice try. You can't pick your own site.")
		return
	}
	picks, _ := db.Picks(v)
	for _, p := range picks {
		if p == id {
			if err := db.RemoveVote(id, v); err != nil {
				fail(w, err)
				return
			}
			buildList(w, r, "")
			return
		}
	}
	// Ignore picks for sites that no longer exist.
	live := 0
	exists := map[string]bool{}
	for _, s := range sites {
		exists[s.ID] = true
	}
	for _, p := range picks {
		if exists[p] {
			live++
		}
	}
	if live >= maxPicks {
		buildList(w, r, "You've used all 3 picks. Un-pick one to change it.")
		return
	}
	if err := db.AddVote(id, v); err != nil {
		fail(w, err)
		return
	}
	buildList(w, r, "")
}

// Board is the host view. GET ?format=json returns the ranking for the reveal.
func Board(w http.ResponseWriter, r *http.Request) {
	if !pinOK(r) {
		http.Error(w, "wrong pin", 401)
		return
	}
	if r.URL.Query().Get("format") == "json" {
		bv, err := board()
		if err != nil {
			fail(w, err)
			return
		}
		type item struct {
			Name    string `json:"name"`
			URL     string `json:"url"`
			Tagline string `json:"tagline"`
			Count   int    `json:"count"`
		}
		out := struct {
			Ranked     []item `json:"ranked"`
			TotalVotes int    `json:"totalVotes"`
			Voters     int    `json:"voters"`
			Locked     bool   `json:"locked"`
		}{TotalVotes: bv.TotalVotes, Voters: bv.Voters, Locked: bv.Locked, Ranked: []item{}}
		for _, s := range bv.Ranked {
			out.Ranked = append(out.Ranked, item{s.Name, s.URL, s.Tagline, s.Count})
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(out)
		return
	}
	buildBoard(w, r)
}

// Admin: POST action=lock|unlock|reset|delete(&id).
func Admin(w http.ResponseWriter, r *http.Request) {
	if !pinOK(r) {
		http.Error(w, "wrong pin", 401)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	r.ParseForm()
	var err error
	switch r.FormValue("action") {
	case "lock":
		err = db.SetState(store.State{Locked: true})
	case "unlock":
		err = db.SetState(store.State{Locked: false})
	case "reset":
		err = db.ResetVotes()
	case "delete":
		err = db.DeleteSite(r.FormValue("id"))
	default:
		http.Error(w, "unknown action", 400)
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	buildBoard(w, r)
}
