---
title: Quick start
description: Connect the Harbor Resource Operator to a Harbor, then create a project and a robot account whose pull secret your Deployments can use.
---

# Quick start

This takes about five minutes on any cluster where the operator is [installed](install.md) and that can reach your Harbor.

## 1. Connect a Harbor

Store the credentials in a Secret, in a namespace only the platform team can read, and point a `HarborConnection` at Harbor:

```bash
kubectl create namespace harbor-system
kubectl -n harbor-system create secret generic prod-harbor-admin \
  --from-literal=username='<harbor user>' --from-literal=password='<password>'
```

```yaml
apiVersion: harbor.oreonit.com/v1alpha1
kind: HarborConnection
metadata:
  name: prod-harbor
  namespace: harbor-system
spec:
  url: https://harbor.example.com
  credentialsSecretRef:
    name: prod-harbor-admin
  allowedNamespaces: ["*"]   # namespaces whose resources may use this connection
```

```bash
kubectl -n harbor-system get hconn
```

The connection becomes `Ready` once Harbor accepts the credentials. If Harbor uses a private CA, set `caBundleSecretRef`; see the [HarborConnection reference](reference/harborconnection.md).

!!! tip "Which Harbor account?"
    System-level kinds (projects, registries, replication, global labels, garbage collection, system robots) need a Harbor administrator or a system robot account with those permissions. The end-to-end suite runs as an administrator; a least-privilege robot setup is not verified yet.

## 2. Create a project and a robot account

```yaml
apiVersion: harbor.oreonit.com/v1alpha1
kind: HarborProject
metadata:
  name: team-payments
  namespace: team-payments
spec:
  connectionRef: { name: prod-harbor, namespace: harbor-system }
  name: payments
  storageQuotaBytes: 53687091200   # 50 GiB
  metadata:
    autoScan: true
    preventVulnerable: true
    severity: critical
---
apiVersion: harbor.oreonit.com/v1alpha1
kind: HarborRobotAccount
metadata:
  name: payments-ci
  namespace: team-payments
spec:
  connectionRef: { name: prod-harbor, namespace: harbor-system }
  name: ci
  level: project
  projectRef: { name: team-payments }
  duration: 90
  permissions:
  - access:
    - { resource: repository, action: push }
    - { resource: repository, action: pull }
  secret:
    name: payments-ci-regcred
    type: kubernetes.io/dockerconfigjson
  rotation:
    beforeExpiry: 168h
```

```bash
kubectl create namespace team-payments
kubectl apply -f project-and-robot.yaml
kubectl -n team-payments get hproj,hrobot
```

Both objects show their Harbor ID and `Ready=True` within seconds. The robot's credentials are in the Secret `payments-ci-regcred`, ready for `imagePullSecrets` or a CI job, and the operator recreates the robot a week before it expires.

## 3. Watch drift correction

Change the project's severity in the Harbor UI and wait for the sync period (10 minutes by default, `syncPeriod` on the connection or the resource). The operator puts it back and records an event:

```bash
kubectl -n team-payments get events --field-selector reason=DriftCorrected
```

## Next

- More examples for every kind are in the [reference](reference/harborproject.md) and in [`config/samples`](https://github.com/oreondev/harbor-resource-operator/tree/main/config/samples).
- Read [how the operator behaves](behaviour.md) before pointing it at a Harbor that already has objects: it only takes them over with `adoptExisting`.
