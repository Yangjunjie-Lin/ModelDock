# Public Beta Kubernetes deployment

This deployment keeps ModelDock application pods stateless and requires an
external PostgreSQL service, an external Redis service, TLS ingress, and a
secret named `modeldock-runtime-secrets`. Populate that Secret through the
platform secret manager/CSI driver or External Secrets controller; do not
commit a Secret manifest. It must provide `DATABASE_URL`,
`MIGRATION_DATABASE_URL`, `REDIS_URL`,
`RELAYDOCK_MASTER_KEY`, `RELAYDOCK_API_KEY_HMAC_SECRET`,
`RELAYDOCK_JWT_SECRET`, `ALLOWED_ORIGINS`,
`RELAYDOCK_PROVIDER_ALLOWED_HOSTS`, and the configured SMTP credentials.

Replace the image with an immutable digest and the example ingress host before
applying. Run the migration Job to completion before updating the Deployment:

```bash
kubectl -n modeldock delete job modeldock-migration --ignore-not-found
kubectl apply -f deploy/kubernetes/public-beta.yaml
kubectl -n modeldock wait --for=condition=complete job/modeldock-migration --timeout=180s
kubectl -n modeldock rollout status deployment/modeldock --timeout=10m
```

The Deployment uses two replicas, `maxUnavailable: 0`, startup/readiness/
liveness probes, a PodDisruptionBudget, a pre-stop load-balancer propagation
delay, and a 120-second termination window. The process then marks readiness
false and uses `http.Server.Shutdown`, so in-flight SSE requests may finish up
to `SHUTDOWN_TIMEOUT`. The Ingress exposes only `/v1`; publish control-plane
routes through a separate authenticated ingress when required.

For a 5% canary, replace `modeldock/server:canary` in `canary.yaml` with the
candidate digest, apply it, watch SLO/error/stream metrics, and delete the
canary Ingress and Deployment on failure. Promote the same verified digest to
the main Deployment only after the observation window.

The included NetworkPolicy limits runtime ports but cannot know managed-service
CIDRs. Narrow its namespace and destination selectors to the ingress
controller and the approved PostgreSQL, Redis, SMTP, OTLP, and Provider CIDRs
before public traffic is admitted.
