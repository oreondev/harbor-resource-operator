---
title: Agent skills in Harbor
description: Set up a Harbor project for agent skills with the Harbor Resource Operator - cosign enforcement, immutable versions, retention, a CI robot that pushes and an agent robot that only reads.
---

# Agent skills in Harbor

Harbor can be the registry for the [agent skills](https://agentskills.io/specification) your team writes: each skill is pushed as an OCI artifact in the [Agent Skills OCI artifacts](https://github.com/ThomasVitale/agents-skills-oci-artifacts-spec) format, one repository per skill, and Harbor shows it with type `SKILL`. The [Harbor MCP server](https://www.oreonit.com/harbor-mcp/latest/skills/) lets an AI assistant list, read and install them.

The manifests below set up such a project as code: a private `skills` project that only serves signed artifacts, version tags that can never be overwritten, the last ten versions of each skill kept, a robot for CI that pushes and signs, and a robot for assistants that can only read. They use the `prod-harbor` connection from the [quick start](quickstart.md); the file is [`config/samples/recipes/agent-skills.yaml`](https://github.com/oreondev/harbor-resource-operator/blob/main/config/samples/recipes/agent-skills.yaml).

```yaml
# A Harbor project for agent skills (Agent Skills OCI artifacts, one
# repository per skill), as https://www.oreonit.com/harbor-operator/latest/skills/
# describes. Uses the prod-harbor HarborConnection from the samples.
apiVersion: harbor.oreonit.com/v1alpha1
kind: HarborProject
metadata:
  name: skills
  namespace: platform
spec:
  connectionRef:
    name: prod-harbor
    namespace: harbor-system
  name: skills
  public: false
  storageQuotaBytes: 1073741824     # skills are small; 1 GiB is plenty
  metadata:
    # Harbor refuses to serve an artifact without a cosign signature.
    enableContentTrustCosign: true
  deletionPolicy: Retain
---
# A released version of a skill can never be overwritten.
apiVersion: harbor.oreonit.com/v1alpha1
kind: HarborImmutableRule
metadata:
  name: skills-versions
  namespace: platform
spec:
  connectionRef:
    name: prod-harbor
    namespace: harbor-system
  projectRef:
    name: skills
  repositories:
    pattern: "**"
  tags:
    decoration: matches
    pattern: "*.*.*"
---
# Keep the last ten versions of every skill; signatures follow their artifact.
apiVersion: harbor.oreonit.com/v1alpha1
kind: HarborRetentionPolicy
metadata:
  name: skills-retention
  namespace: platform
spec:
  connectionRef:
    name: prod-harbor
    namespace: harbor-system
  projectRef:
    name: skills
  rules:
  - template: latestPushedK
    count: 10
    repositories:
      pattern: "**"
    tags:
      pattern: "**"
      untagged: false
  schedule: "0 0 3 * * *"
---
# CI pushes and signs skills.
apiVersion: harbor.oreonit.com/v1alpha1
kind: HarborRobotAccount
metadata:
  name: skills-ci
  namespace: platform
spec:
  connectionRef:
    name: prod-harbor
    namespace: harbor-system
  name: skills-ci
  level: project
  projectRef:
    name: skills
  description: Pushes and signs agent skills
  duration: 90
  permissions:
  - access:
    - { resource: repository, action: push }
    - { resource: repository, action: pull }
    - { resource: artifact, action: read }
  secret:
    name: skills-ci-regcred
    type: kubernetes.io/dockerconfigjson
  rotation:
    beforeExpiry: 168h
---
# Assistants read skills through the Harbor MCP server: list, read and pull,
# nothing else. The Secret has the username and password keys harbor-mcp
# reads as HARBOR_USERNAME and HARBOR_PASSWORD.
apiVersion: harbor.oreonit.com/v1alpha1
kind: HarborRobotAccount
metadata:
  name: skills-agent
  namespace: platform
spec:
  connectionRef:
    name: prod-harbor
    namespace: harbor-system
  name: skills-agent
  level: project
  projectRef:
    name: skills
  description: Reads agent skills for harbor-mcp
  duration: 90
  permissions:
  - access:
    - { resource: repository, action: list }
    - { resource: repository, action: read }
    - { resource: repository, action: pull }
    - { resource: artifact, action: list }
    - { resource: artifact, action: read }
    - { resource: tag, action: list }
  secret:
    name: skills-agent-harbor
    type: Opaque
  rotation:
    beforeExpiry: 168h
```

What each part does:

- **`enableContentTrustCosign: true`**: Harbor refuses to serve any artifact in the project without a cosign signature, so an unsigned or tampered skill never reaches an assistant. Sign in CI with `cosign sign` after the push.
- **The immutable rule** stops anyone, including CI, from pushing a different skill under an existing version tag such as `1.2.0`. Use `latest` or other non-version tags for moving pointers.
- **The retention policy** keeps the ten newest versions of each skill and runs nightly.
- **`skills-ci`** writes a `kubernetes.io/dockerconfigjson` Secret for the pipeline that pushes and signs.
- **`skills-agent`** writes an Opaque Secret with `username` and `password`, which harbor-mcp reads as `HARBOR_USERNAME` and `HARBOR_PASSWORD`. It can list, read and pull in this project only. Both robots expire after 90 days and are rotated a week before.

Harbor's vulnerability scanners only handle images, so scan skills in CI before the push.
