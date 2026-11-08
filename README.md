# Site Showdown

A class vote for the end of the "Build your own website with AI" session. Students add their finished site and pick three favorites. The host watches the votes land live, then runs a three-step reveal that ends with the winner going kaboom.

Go + htmx. No framework, no build step. Hosted on Vercel.

## Pages

| URL | Who | What |
|---|---|---|
| `/` | Students | Add your site, pick up to 3 favorites. Votes are hidden. |
| `/host` | You | Live standings, previews of every site, open/close voting, reset, remove entries. Needs the pin. |
| `/reveal` | You, on the big screen | 3rd → 2nd → drum roll → winner, confetti and all. Space or → to advance, R to replay, F for full screen. |

## Deploy (about five minutes)

1. **Import the repo in Vercel.** vercel.com → Add New → Project → pick `site-showdown`. Leave every setting as it is (no framework) and click Deploy. It builds the Go functions under `api/` and serves `public/`.
2. **Add a database.** In the project, open **Storage → Create Database → Upstash for Redis** (free tier). Accept the defaults. Vercel adds `KV_REST_API_URL` and `KV_REST_API_TOKEN` to the project for you.
3. **Set your pin.** Settings → Environment Variables → add `HOST_PIN` with a value only you know. Without it the pin is `kaboom`.
4. **Redeploy** (Deployments → ⋯ on the latest → Redeploy) so the new variables take effect.

Then share `https://<your-project>.vercel.app/` with students and open `/host` yourself.

## Run it on your laptop

```
go run ./cmd/dev
```

Open http://localhost:8080. Votes live in memory until you stop it. `HOST_PIN=1234 go run ./cmd/dev` sets the pin.

## How it works

- `api/*/index.go` — one tiny Vercel function per route, each calling into `app/`.
- `app/app.go` — the handlers. They return HTML fragments that htmx swaps into the page.
- `store/store.go` — `Memory` for local runs, `Upstash` (Redis over REST) on Vercel. Picked automatically from the environment.
- `public/` — the three pages and one stylesheet.

Each browser gets a random id in `localStorage`. It marks the site you submitted (so you can remove it and can't vote for it) and holds your picks. A determined student could clear storage and vote again; for a class vote that's fine.

## During class

- Open `/host` before students arrive and keep it on a second screen. It refreshes every 2 seconds.
- Click **Close voting** before the reveal, or the reveal page will say voting is still open.
- Press **F** on the reveal page for full screen, then Space to step through.
- **Reset votes** wipes votes but keeps the sites, handy for a practice run.
