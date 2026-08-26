# Repository aliases

This directory holds the source registry for ghget's built-in repository
aliases. Each non-comment line in `aliases.tsv` contains an alias followed by
an `OWNER/REPO` value, an optional asset hint, and an optional source backend.

Any non-empty run of whitespace may separate the fields:

```text
fd      sharkdp/fd
rg      BurntSushi/ripgrep
kubens  ahmetb/kubectx  kubens
kubectl kubernetes/kubectl kubectl kubernetes
```

The asset hint identifies the requested program when one repository publishes
separate release assets for several tools. Without it, automatic selection uses
the repository name as before.

The backend selects a trusted download source other than GitHub Releases. A
backend entry must also provide an asset hint, which identifies the artifact at
that source. Backend names are compiled into ghget; registry data cannot provide
an arbitrary download URL.

Aliases are case-insensitive and must be unique. Blank lines and lines whose
first non-whitespace character is `#` are ignored.

Each tool group must have a one-line comment describing the project. When a
tool has multiple common executable names, put its aliases together below a
single description.

Keep tools that require an unsupported download backend as commented entries.
Add a nearby comment explaining why they are disabled and link the tracking
issue when one exists. Commented entries are excluded from the embedded
registry.

After editing the registry, regenerate its compressed embedded form:

```sh
go generate ./internal/repoalias
```

Tests verify that the embedded registry matches this source file.
