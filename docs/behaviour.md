---
title: How it behaves
description: Managed fields, drift correction, adoption of existing Harbor objects, deletion policies, robot secrets, multi-tenancy and rate limiting in the Harbor Resource Operator.
---

# How it behaves

## Managed fields only

Only fields you set in `spec` are managed. Fields you leave unset are left as Harbor has them, so you can adopt an existing project and manage just its quota.

## Drift correction

Every `syncPeriod` (default 10 minutes, set on the connection or per resource) the operator re-reads Harbor and restores the declared values, emitting a `DriftCorrected` event. Objects deleted in Harbor behind its back are recreated.

## Finding and adopting objects

The operator finds a Harbor object by the ID stored in `status` first, then by name. If an object of that name already exists in Harbor and was not created by this resource, the resource reports `AlreadyExists` and changes nothing until you set `spec.adoptExisting: true`. Adopting a robot account refreshes its secret, because Harbor never returns an existing secret.

## Deletion

Every kind has `deletionPolicy`:

- `Delete` removes the Harbor object when the Kubernetes object is deleted. A Harbor 404 counts as success.
- `Retain` leaves it in Harbor.

`HarborProject` and `HarborGarbageCollection` default to `Retain`; the other kinds default to `Delete`. A project with `Delete` is only removed once it holds no repositories: until then the object stays with reason `ProjectNotEmpty`, and it is deleted as soon as the last repository is gone. The operator never deletes repositories or artifacts.

## Robot secrets

Harbor only returns a robot secret when the robot is created. The operator writes it to the Secret immediately. If the Secret is deleted or loses its password, the robot secret is refreshed and the Secret rewritten. `rotation.period` refreshes on a schedule; `rotation.beforeExpiry` recreates the robot before it expires. Secrets never appear in status, events or logs.

## Multi-tenancy

A resource may reference a connection in another namespace only if that connection lists the namespace (or `*`) in `allowedNamespaces`. Keep the credentials Secret in the connection's namespace and give application teams access only to their own namespaces.

## Project-scoped kinds

Members, labels, immutable rules, webhooks, retention policies and project robots reference a `HarborProject` with `projectRef` (it must use the same connection), or name an existing Harbor project with `projectName`. The project and the member identity are immutable; recreate the object to change them.

## Rate limiting

Each connection has one token bucket (`requestsPerSecond`, default 10) shared by all controllers, so a large GitOps sync does not overload Harbor.

## One garbage collection schedule per Harbor

If several `HarborGarbageCollection` objects point at one connection, the oldest wins and the others report `Duplicate`.

## Status and events

Every resource has a `Ready` condition with a reason (`Synced`, `AlreadyExists`, `DependencyNotReady`, `ConnectionUnavailable`, `ProjectNotEmpty`, ...) and a human-readable message, the Harbor ID in `status`, and Kubernetes events for creates, updates, adoption and drift corrections.

## Single-namespace installs

`--watch-namespaces=a,b` restricts the manager cache, and so the operator, to those namespaces.
