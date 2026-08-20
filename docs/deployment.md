# Kubernetes deployment

The baseline manifests in `deploy/kubernetes` run three restricted API replicas behind TLS ingress, with startup/liveness/readiness probes, rolling updates, autoscaling, disruption protection, resource limits, and default-deny application networking. They intentionally do not deploy PostgreSQL, Redis, Redis Commander, or an observability backend.

## Release procedure

1. Build, scan, sign, and push the production image. Record its immutable `repository@sha256:...` reference.
2. Create `cinema-secrets` from the organization secret manager. `secret.example.yaml` lists required keys but must never be applied with placeholder values or committed with real values.
3. Set the public host, TLS secret, proxy CIDRs, private service endpoints, and provider-specific egress rules for the target environment.
4. Replace both `cinema-api:replace-with-immutable-digest` image references with the same digest. Run `kubectl kustomize deploy/kubernetes` and review the result.
5. Apply the namespace, account, configuration, secret, and network policy first.
6. Apply `migration-job.yaml` as a uniquely named pre-deployment Job and wait for successful completion. Never run multiple migration Jobs concurrently.
7. Apply the remaining kustomization, watch `kubectl rollout status deployment/cinema-api -n cinema`, and exercise live, ready, metrics, sign-in, seat hold, and test-payment checks.

Example image replacement and validation:

```sh
kubectl kustomize deploy/kubernetes > /tmp/cinema-rendered.yaml
kubectl apply --server-side --dry-run=server -f /tmp/cinema-rendered.yaml
kubectl rollout status deployment/cinema-api -n cinema --timeout=5m
```

Use a deployment tool such as Kustomize overlays, Helm, Argo CD, or Flux to inject the immutable image and environment-specific hostnames. Do not perform ad-hoc substitutions in production terminals.

## Rollback

Application rollbacks use the previous immutable image digest. Database migrations are forward-only, so verify that the prior application version is compatible with the migrated schema before rolling it back. If it is not, roll forward with a corrective release. See `database-operations.md` for migration and restore policy.

## Network and secret notes

The baseline egress policy permits common PostgreSQL, Redis, SMTP, and HTTPS ports because standard Kubernetes NetworkPolicy cannot select external services by DNS name. Tighten CIDRs for the actual provider or use a CNI with FQDN policies. Keep cloud metadata ranges denied. Adjust ingress namespace labels to match the installed ingress and monitoring controllers.

Mount private Redis CA or mTLS material from a separate Secret and set the corresponding file environment variables when the provider does not use a public CA. Prefer an external-secrets controller or CSI secret-store driver; Kubernetes Secret objects alone are only base64 encoded.
