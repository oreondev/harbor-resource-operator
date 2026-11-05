---
title: Metrics
description: Prometheus metrics exported by the Harbor Resource Operator, including Harbor API latency, drift corrections and robot account expiry.
---

# Metrics

The manager serves Prometheus metrics on port 8443 over HTTPS; scraping needs a token with access to the `/metrics` non-resource URL. With the Helm chart, `prometheus.enabled: true` creates a ServiceMonitor.

Besides the controller-runtime reconcile metrics, the operator exports:

| Metric | Labels | Meaning |
|---|---|---|
| `harbor_api_requests_total` | `connection`, `method`, `code` | Requests to Harbor by status code |
| `harbor_api_request_duration_seconds` | `connection`, `method` | Harbor API latency |
| `harbor_drift_corrections_total` | `kind` | Values restored after a change in Harbor |
| `harbor_resources` | `kind`, `connection`, `ready` | Managed resources by readiness |
| `harbor_robot_expiration_timestamp_seconds` | `namespace`, `name`, `robot` | When each robot account expires |

## Example alerts

```yaml
- alert: HarborRobotExpiresSoon
  expr: harbor_robot_expiration_timestamp_seconds - time() < 7 * 86400
  labels: { severity: warning }
  annotations:
    summary: "Robot {{ $labels.robot }} expires within a week"
- alert: HarborResourcesNotReady
  expr: sum by (kind, connection) (harbor_resources{ready="false"}) > 0
  for: 30m
  labels: { severity: warning }
```
