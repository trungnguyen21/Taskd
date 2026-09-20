# Kubernetes manifests

`docker compose up` is the supported way to run Taskd. These manifests are the
scale-out path, and are what the project uses to exercise the distributed
design.

## Applying

```sh
kubectl apply -f 00-namespace.yaml

# Create the real secret first; do not apply the placeholder in 01-config.yaml.
kubectl -n taskd create secret generic taskd-secrets \
  --from-literal=POSTGRES_PASSWORD="$(openssl rand -base64 24)" \
  --from-literal=TASKD_SECRET_KEY="$(openssl rand -base64 32)" \
  --from-literal=TASKD_PASSWORD="the dashboard password" \
  --from-literal=TASKD_SEARCH_API_KEY=""

kubectl apply -f 01-config.yaml   # ConfigMap only, if the secret already exists
kubectl apply -f 02-postgres.yaml
kubectl apply -f 03-coordinator.yaml
kubectl apply -f 04-worker.yaml
kubectl apply -f 05-api.yaml
```

Scale workers with `kubectl -n taskd scale deploy/worker --replicas=4`.

## What to know before running this

**`TASKD_SECRET_KEY` seals every credential stored through the dashboard.**
Losing it, or changing it, means losing all of them. Back it up somewhere other
than the cluster.

**The coordinator runs one replica.** Its worker registry is held in memory, so
two replicas would each see half the fleet and dispatch to half the workers.
Moving the registry into Postgres is what lifts that limit; until then the
Deployment uses the `Recreate` strategy so two never run at once.

**There is no scale-to-zero.** Dispatch is push-based: the coordinator dials
workers directly, so a worker must be running and registered to receive
anything. Workers are paid for around the clock even if agents run three times a
day. This is a deliberate trade for the distributed design, and it is why
`docker compose` is the recommended path for a personal install.

**Workers get a 660-second grace period.** A worker asked to stop declines new
runs and finishes what it already accepted. Killing it sooner abandons a run the
coordinator believes is in flight, which then waits out its lease before being
failed. Raise the grace period if you raise agents' maximum duration budget.

**Images are placeholders.** Point them at wherever you publish; the Dockerfiles
in the repository root build them.
