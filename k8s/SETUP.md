# Running the rate limiter on OrbStack's Kubernetes (step by step)

You don't need `kind`, `minikube`, or anything else — OrbStack ships its
own lightweight Kubernetes cluster, and it uses the *same* image store as
`docker build`, so there's no push/load step. This guide assumes zero
prior Kubernetes knowledge.

## Two-sentence Kubernetes primer, so the steps make sense

- A **Pod** is one running copy of your container (like `docker run`, but
  managed by Kubernetes). A **Deployment** says "keep N Pods of this image
  running" — that's how you get 3 independent copies of your app.
- A **Service** is a stable network address that load-balances across all
  the Pods a Deployment creates, so callers don't need to know which Pod
  they're hitting.

That's really all you need for this exercise.

## 1. Turn on Kubernetes in OrbStack

Either:
- OrbStack app → Settings → Kubernetes → toggle it on, **or**
- In a terminal: `orb start k8s`

Give it ~30 seconds the first time. Then check it's alive:

```bash
kubectl get nodes
```

You should see one node in `Ready` status. (`kubectl` comes bundled with
OrbStack — nothing extra to install. If this command isn't found, your
shell may need `source ~/.orbstack/shell/init.zsh` in a new terminal, or
just restart your terminal.)

## 2. Build the image

From the project root (where the `Dockerfile` is):

```bash
docker build -t ratelimiter:dev .
```

That's it — no `kind load` step. Because OrbStack's Kubernetes and Docker
share the same image store, the moment `docker build` finishes, that
image is already usable in a Pod. (Note the tag is `:dev`, not `:latest`
— Kubernetes always tries to re-pull `:latest` from a registry, which
would fail for a local-only image.)

## 3. Deploy Postgres, Redis, and your app

Run these one at a time, from the project root:

```bash
kubectl apply -f k8s/00-namespace.yaml
kubectl apply -f k8s/01-postgres.yaml
kubectl apply -f k8s/02-redis.yaml
kubectl apply -f k8s/03-app-config.yaml
kubectl apply -f k8s/04-app.yaml
```

Watch things come up:

```bash
kubectl -n ratelimiter get pods --watch
```

(`Ctrl+C` once everything says `Running` and `1/1` or `3/3` Ready — this
usually takes under a minute.)

## 4. Seed the database

Your Postgres `endpoints` table needs data before any `/api/...` route
will work. Wait for Postgres specifically, then run the seed Job:

```bash
kubectl -n ratelimiter wait --for=condition=ready pod -l app=postgres --timeout=60s
kubectl apply -f k8s/05-seed-job.yaml
kubectl -n ratelimiter wait --for=condition=complete job/ratelimiter-seed --timeout=60s
```

Confirm it worked:

```bash
kubectl -n ratelimiter logs job/ratelimiter-seed
```

You should see something like `Inserted 1000 endpoints.`

## 5. Check you really have 3 separate pods

```bash
kubectl -n ratelimiter get pods -o wide
```

You'll see 3 pods named `ratelimiter-xxxxx-yyyyy`, each with its own IP.
These are 3 independent OS processes — nothing is shared between them
except Redis and Postgres. That's the setup you actually wanted to test.

## 6. Point traffic at it

```bash
kubectl -n ratelimiter port-forward svc/ratelimiter 8080:8080
```

Leave that running in its own terminal — it forwards `localhost:8080` to
the `ratelimiter` Service, which load-balances across all 3 pods. In a
second terminal, sanity-check it:

```bash
curl -i http://localhost:8080/health
curl -i -H "X-User-Id: 5" http://localhost:8080/api/v1/auth/profile/read
```

(That path is one the seed script generates — check
`kubectl -n ratelimiter logs job/ratelimiter-seed` or query Postgres
directly if you want to pick a specific one.)

## 7. Run the real load test

Your `load-test.js` already targets `localhost:8080`, so with the
port-forward from step 6 still running:

```bash
k6 run load-test.js
```

## 8. What you're actually checking

This is the part that matters more than "did it run":

- Pick one `X-User-Id` + one endpoint path from the seed data.
- Send that user's requests fast enough that they land on all 3 pods
  (the Service round-robins, so a burst of requests will spread out).
- Count the 200s you got back. It should track that endpoint's
  `max_limit` (plus whatever lazy refill happens during the test) —
  **not** `max_limit × 3`.
- If you see roughly 3x too many allowed requests, that means each pod
  is enforcing its own limit instead of sharing state through Redis —
  which is exactly the bug class this whole test is designed to catch.
  Given your `redis.go` uses a single atomic Lua script for the
  check-and-refill, this should hold — but that's the claim actually
  worth verifying under real concurrency rather than assuming.
- While it's running, check `curl http://localhost:8080/metrics` (via
  the same port-forward) for `redis_eval_duration` numbers — real
  latency under 3-pod concurrent load, which is more meaningful than
  numbers from a single process.

## 9. Cleaning up

```bash
kubectl delete namespace ratelimiter
```

This deletes everything you created (Pods, Services, the Job) in one
shot. Kubernetes itself keeps running in OrbStack until you turn it off
again (Settings → Kubernetes, or `orb stop k8s`).

## If something doesn't come up

```bash
kubectl -n ratelimiter get pods
kubectl -n ratelimiter describe pod <pod-name>   # shows why a pod is stuck/crashing
kubectl -n ratelimiter logs <pod-name>           # shows your app's stdout/stderr
```

Common first-timer snags:
- **`ImagePullBackOff`** — almost always means the image tag doesn't
  match what you built, or you rebuilt with a different tag than
  `ratelimiter:dev`. Re-run `docker build -t ratelimiter:dev .` and
  `kubectl -n ratelimiter rollout restart deployment/ratelimiter`.
- **App pods stuck `Pending`/crashing before Postgres is ready** — the
  app tries to connect to Postgres at startup and exits if it can't
  (see `main.go`'s `log.Fatalf`). Just let Kubernetes retry — it
  restarts crashed pods automatically — or re-run step 3's `kubectl
  apply -f k8s/04-app.yaml` after Postgres is confirmed `Ready`.
