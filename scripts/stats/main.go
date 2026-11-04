// Command stats renders assets/stats-{dark,light}.svg from the GitHub GraphQL API.
//
// Run daily by profile-stats.timer on CT203 lxc-personal (/opt/profile-stats/run.sh), from the repo root.
// Env: GH_TOKEN (fine-grained, read-only, all repos: needed to count private ones),
//
//	AUTHOR_EMAILS (optional, comma separated; counts commits made with
//	addresses that are not linked to the account).
//
// Build: cd scripts/stats && CGO_ENABLED=0 go build -trimpath -ldflags=-s -o stats .
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

var token string

func gql(query string, vars map[string]any, out any) {
	body, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	client := &http.Client{Timeout: 60 * time.Second}
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ { // GitHub GraphQL sometimes answers 502/504 on heavy queries
		if attempt > 0 {
			time.Sleep(time.Duration(15*attempt) * time.Second)
		}
		req, _ := http.NewRequest("POST", "https://api.github.com/graphql", bytes.NewReader(body))
		req.Header.Set("Authorization", "bearer "+token)
		req.Header.Set("User-Agent", "profile-stats")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		raw := new(bytes.Buffer)
		raw.ReadFrom(resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("github: HTTP %d", resp.StatusCode)
			continue
		}
		if resp.StatusCode != 200 {
			log.Fatalf("github: HTTP %d", resp.StatusCode)
		}
		var env struct {
			Data   json.RawMessage `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := json.Unmarshal(raw.Bytes(), &env); err != nil {
			log.Fatal(err)
		}
		if len(env.Errors) > 0 {
			log.Fatalf("github: %s", env.Errors[0].Message)
		}
		if err := json.Unmarshal(env.Data, out); err != nil {
			log.Fatal(err)
		}
		return
	}
	log.Fatal(lastErr)
}

type repo struct {
	IsFork    bool `json:"isFork"`
	IsPrivate bool `json:"isPrivate"`
	Languages struct {
		Edges []struct {
			Size int64 `json:"size"`
			Node struct {
				Name  string  `json:"name"`
				Color *string `json:"color"`
			} `json:"node"`
		} `json:"edges"`
	} `json:"languages"`
	DefaultBranchRef *struct {
		Target struct {
			History *struct {
				TotalCount int `json:"totalCount"`
			} `json:"history"`
		} `json:"target"`
	} `json:"defaultBranchRef"`
}

// thousands formats n as 1,234 (matches Python's f"{n:,}").
func thousands(n int) string {
	s := strconv.Itoa(n)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

func main() {
	outDir := flag.String("out", "assets", "directory for stats-{dark,light}.svg")
	flag.Parse()
	token = os.Getenv("GH_TOKEN")
	if token == "" {
		log.Fatal("GH_TOKEN not set")
	}
	var emails []string
	for _, e := range strings.Split(os.Getenv("AUTHOR_EMAILS"), ",") {
		if e = strings.TrimSpace(e); e != "" {
			emails = append(emails, e)
		}
	}

	var v struct {
		Viewer struct {
			ID string `json:"id"`
		} `json:"viewer"`
	}
	gql("{viewer{id login createdAt}}", nil, &v)
	author := map[string]any{"id": v.Viewer.ID}
	if len(emails) > 0 {
		author = map[string]any{"emails": emails}
	}

	var repos []repo
	var cursor any
	for {
		var page struct {
			Viewer struct {
				Repositories struct {
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
					Nodes []repo `json:"nodes"`
				} `json:"repositories"`
			} `json:"viewer"`
		}
		gql(`query($after:String,$author:CommitAuthor){viewer{repositories(ownerAffiliations:OWNER,first:50,after:$after){
             pageInfo{hasNextPage endCursor}
             nodes{isFork isPrivate
               languages(first:20){edges{size node{name color}}}
               defaultBranchRef{target{... on Commit{history(author:$author){totalCount}}}}}}}}`,
			map[string]any{"after": cursor, "author": author}, &page)
		r := page.Viewer.Repositories
		repos = append(repos, r.Nodes...)
		if !r.PageInfo.HasNextPage {
			break
		}
		cursor = r.PageInfo.EndCursor
	}

	commits, private := 0, 0
	langs := map[string]int64{}
	colors := map[string]string{}
	var order []string // first-seen order, so ties sort like Python's Counter.most_common
	for _, r := range repos {
		if r.IsPrivate {
			private++
		}
		mine := 0
		if r.DefaultBranchRef != nil && r.DefaultBranchRef.Target.History != nil {
			mine = r.DefaultBranchRef.Target.History.TotalCount
		}
		if r.IsFork || mine == 0 {
			continue
		}
		commits += mine
		for _, e := range r.Languages.Edges {
			n := e.Node.Name
			if _, ok := langs[n]; !ok {
				order = append(order, n)
			}
			langs[n] += e.Size
			colors[n] = "#8b949e"
			if e.Node.Color != nil && *e.Node.Color != "" {
				colors[n] = *e.Node.Color
			}
		}
	}

	now := time.Now().UTC()
	var y struct {
		Viewer struct {
			ContributionsCollection struct {
				ContributionCalendar struct {
					TotalContributions int `json:"totalContributions"`
				} `json:"contributionCalendar"`
			} `json:"contributionsCollection"`
		} `json:"viewer"`
	}
	gql(`query($from:DateTime!){viewer{contributionsCollection(from:$from){contributionCalendar{totalContributions}}}}`,
		map[string]any{"from": fmt.Sprintf("%d-01-01T00:00:00Z", now.Year())}, &y)
	year := y.Viewer.ContributionsCollection.ContributionCalendar.TotalContributions

	stats := [][2]string{
		{thousands(commits), "commits"},
		{thousands(year), fmt.Sprintf("contributions in %d", now.Year())},
		{strconv.Itoa(len(repos)), fmt.Sprintf("repositories, %d private", private)},
		{strconv.Itoa(len(langs)), "languages written"},
	}

	var total int64
	for _, s := range langs {
		total += s
	}
	if total == 0 {
		log.Fatal("no language data")
	}
	sort.SliceStable(order, func(i, j int) bool { return langs[order[i]] > langs[order[j]] })
	top := order
	if len(top) > 6 {
		top = top[:6]
	}
	type seg struct {
		name  string
		share float64
		color string
	}
	var segs []seg
	var topSum int64
	for _, n := range top {
		segs = append(segs, seg{n, float64(langs[n]) / float64(total), colors[n]})
		topSum += langs[n]
	}
	if other := total - topSum; other > 0 {
		segs = append(segs, seg{"Other", float64(other) / float64(total), "#6e7681"})
	}

	themes := []struct{ name, fg, muted, line string }{
		{"dark", "#e6edf3", "#8b949e", "#30363d"},
		{"light", "#1f2328", "#59636e", "#d0d7de"},
	}
	const font = "-apple-system,BlinkMacSystemFont,'Segoe UI','Noto Sans',Helvetica,Arial,sans-serif"
	f1 := func(x float64) string { return strconv.FormatFloat(x, 'f', 1, 64) }

	for _, t := range themes {
		var o, bar, legend strings.Builder
		for i, s := range stats {
			cx := 105 + i*210
			fmt.Fprintf(&o, `<g class="in" style="animation-delay:%.2fs"><text x="%d" y="40" class="n">%s</text><text x="%d" y="64" class="k">%s</text></g>`,
				float64(i)*.12, cx, s[0], cx, s[1])
			if i > 0 {
				fmt.Fprintf(&o, `<path d="M%d 14V70" stroke="%s"/>`, i*210, t.line)
			}
		}
		x := 0.0
		for _, s := range segs {
			w := s.share * 840
			fmt.Fprintf(&bar, `<rect x="%s" y="100" width="%s" height="8" fill="%s"/>`, f1(x), f1(max(w-2, 1)), s.color)
			x += w
		}
		lx := 0.0
		for _, s := range segs {
			pct := f1(s.share * 100)
			txt := s.name + " " + pct + "%"
			fmt.Fprintf(&legend, `<circle cx="%s" cy="131" r="5" fill="%s"/><text x="%s" y="135.5" class="g"><tspan font-weight="600" fill="%s">%s</tspan> %s%%</text>`,
				f1(lx+5), s.color, f1(lx+15), t.fg, s.name, pct)
			lx += 22 + float64(len(txt))*7.1
		}
		svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="840" height="150" viewBox="0 0 840 150">
<style>
text{font-family:%s}
.n{font-size:28px;font-weight:600;fill:%s;text-anchor:middle}
.k{font-size:13px;fill:%s;text-anchor:middle}
.g{font-size:12px;fill:%s}
.in{animation:in .8s ease-out backwards}
.bar{transform-box:fill-box;transform-origin:left;animation:grow 1.4s cubic-bezier(.2,.7,.2,1) .3s backwards}
@keyframes in{from{opacity:0;transform:translateY(6px)}}
@keyframes grow{from{transform:scaleX(0)}}
</style>
<defs><clipPath id="r"><rect y="100" width="840" height="8" rx="4"/></clipPath></defs>
%s
<g clip-path="url(#r)"><g class="bar">%s</g></g>
%s
</svg>
`, font, t.fg, t.muted, t.muted, o.String(), bar.String(), legend.String())
		if err := os.WriteFile(filepath.Join(*outDir, "stats-"+t.name+".svg"), []byte(svg), 0o644); err != nil {
			log.Fatal(err)
		}
	}
	parts := make([]string, len(segs))
	for i, s := range segs {
		parts[i] = s.name + " " + f1(s.share*100)
	}
	fmt.Printf("%v | %s\n", stats, strings.Join(parts, ", "))
}
