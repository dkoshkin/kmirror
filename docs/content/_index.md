+++
title = "kmirror"

# [[cascade]]
# type = "blog"
# toc_root = true

#   [cascade._target]
#   path = "/blog/**"

[[cascade]]
type = "docs"

  [cascade._target]
  path = "/**"
+++

kmirror swaps a running in-cluster Kubernetes controller for a binary you built locally, without modifying the
Deployment. It wraps [mirrord](https://github.com/metalbear-co/mirrord) to run your binary with the Pod's network,
DNS, environment and ServiceAccount, takes over the controller's leader-election Lease so only your local copy
reconciles, and hands control back when you exit.

See the [README](https://github.com/dkoshkin/kmirror#readme) for usage.
