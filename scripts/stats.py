"""Render assets/stats-{dark,light}.svg from the GitHub GraphQL API.

Env: GH_TOKEN (needs read access to private repos to count them),
     AUTHOR_EMAILS (optional, comma separated; counts commits made with
     addresses that are not linked to the account).
"""
import collections, datetime, json, os, urllib.request

TOKEN = os.environ["GH_TOKEN"]
EMAILS = [e.strip() for e in os.environ.get("AUTHOR_EMAILS", "").split(",") if e.strip()]
OUT = os.path.join(os.path.dirname(__file__), "..", "assets")


def gql(query, **variables):
    req = urllib.request.Request(
        "https://api.github.com/graphql",
        data=json.dumps({"query": query, "variables": variables}).encode(),
        headers={"Authorization": f"bearer {TOKEN}", "User-Agent": "profile-stats"},
    )
    data = json.load(urllib.request.urlopen(req))
    if "errors" in data:
        raise SystemExit(data["errors"])
    return data["data"]


viewer = gql("{viewer{id login createdAt}}")["viewer"]
author = {"emails": EMAILS} if EMAILS else {"id": viewer["id"]}

repos, cursor = [], None
while True:
    page = gql(
        """query($after:String,$author:CommitAuthor){viewer{repositories(ownerAffiliations:OWNER,first:50,after:$after){
             pageInfo{hasNextPage endCursor}
             nodes{isFork isPrivate
               languages(first:20){edges{size node{name color}}}
               defaultBranchRef{target{... on Commit{history(author:$author){totalCount}}}}}}}}""",
        after=cursor, author=author,
    )["viewer"]["repositories"]
    repos += page["nodes"]
    if not page["pageInfo"]["hasNextPage"]:
        break
    cursor = page["pageInfo"]["endCursor"]

commits, langs, colors = 0, collections.Counter(), {}
for r in repos:
    mine = ((r["defaultBranchRef"] or {}).get("target") or {}).get("history", {}).get("totalCount", 0)
    if r["isFork"] or not mine:
        continue
    commits += mine
    for e in r["languages"]["edges"]:
        langs[e["node"]["name"]] += e["size"]
        colors[e["node"]["name"]] = e["node"]["color"] or "#8b949e"

now = datetime.datetime.now(datetime.timezone.utc)
year = gql(
    'query($from:DateTime!){viewer{contributionsCollection(from:$from){contributionCalendar{totalContributions}}}}',
    **{"from": f"{now.year}-01-01T00:00:00Z"},
)["viewer"]["contributionsCollection"]["contributionCalendar"]["totalContributions"]

stats = [
    (f"{commits:,}", "commits"),
    (f"{year:,}", f"contributions in {now.year}"),
    (f"{len(repos)}", f"repositories, {sum(r['isPrivate'] for r in repos)} private"),
    (f"{len(langs)}", "languages written"),
]
total = sum(langs.values())
top = langs.most_common(6)
other = total - sum(v for _, v in top)
segments = [(n, v / total, colors[n]) for n, v in top] + ([("Other", other / total, "#6e7681")] if other else [])

THEMES = {
    "dark": dict(fg="#e6edf3", muted="#8b949e", line="#30363d"),
    "light": dict(fg="#1f2328", muted="#59636e", line="#d0d7de"),
}
FONT = "-apple-system,BlinkMacSystemFont,'Segoe UI','Noto Sans',Helvetica,Arial,sans-serif"

for name, t in THEMES.items():
    o = []
    for i, (value, label) in enumerate(stats):
        cx = 105 + i * 210
        o.append(f'<g class="in" style="animation-delay:{i * .12:.2f}s">'
                 f'<text x="{cx}" y="40" class="n">{value}</text><text x="{cx}" y="64" class="k">{label}</text></g>')
        if i:
            o.append(f'<path d="M{i * 210} 14V70" stroke="{t["line"]}"/>')
    x = 0.0
    bar = []
    for n, share, c in segments:
        w = share * 840
        bar.append(f'<rect x="{x:.1f}" y="100" width="{max(w - 2, 1):.1f}" height="8" fill="{c}"/>')
        x += w
    lx = 0
    legend = []
    for n, share, c in segments:
        txt = f"{n} {share * 100:.1f}%"
        legend.append(f'<circle cx="{lx + 5}" cy="131" r="5" fill="{c}"/><text x="{lx + 15}" y="135.5" class="g">'
                      f'<tspan font-weight="600" fill="{t["fg"]}">{n}</tspan> {share * 100:.1f}%</text>')
        lx += 22 + len(txt) * 7.1
    svg = f"""<svg xmlns="http://www.w3.org/2000/svg" width="840" height="150" viewBox="0 0 840 150">
<style>
text{{font-family:{FONT}}}
.n{{font-size:28px;font-weight:600;fill:{t["fg"]};text-anchor:middle}}
.k{{font-size:13px;fill:{t["muted"]};text-anchor:middle}}
.g{{font-size:12px;fill:{t["muted"]}}}
.in{{animation:in .8s ease-out backwards}}
.bar{{transform-box:fill-box;transform-origin:left;animation:grow 1.4s cubic-bezier(.2,.7,.2,1) .3s backwards}}
@keyframes in{{from{{opacity:0;transform:translateY(6px)}}}}
@keyframes grow{{from{{transform:scaleX(0)}}}}
</style>
<defs><clipPath id="r"><rect y="100" width="840" height="8" rx="4"/></clipPath></defs>
{"".join(o)}
<g clip-path="url(#r)"><g class="bar">{"".join(bar)}</g></g>
{"".join(legend)}
</svg>
"""
    with open(os.path.join(OUT, f"stats-{name}.svg"), "w") as f:
        f.write(svg)
print(stats, [(n, round(s * 100, 1)) for n, s, _ in segments])
