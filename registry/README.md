# Repository aliases

This directory holds the source registry for ghget's built-in repository
aliases. Each non-comment line in `aliases.tsv` contains an alias followed by
an `OWNER/REPO` value and an optional asset hint.

Any non-empty run of whitespace may separate the fields:

```text
fd      sharkdp/fd
rg      BurntSushi/ripgrep
kubens  ahmetb/kubectx  kubens
```

The asset hint identifies the requested program when one repository publishes
separate release assets for several tools. Without it, automatic selection uses
the repository name as before.

Aliases are case-insensitive and must be unique. Blank lines and lines whose
first non-whitespace character is `#` are ignored.

Each tool group must have a one-line comment describing the project. When a
tool has multiple common executable names, put its aliases together below a
single description.

After editing the registry, regenerate its compressed embedded form:

```sh
go generate ./internal/repoalias
```

Tests verify that the embedded registry matches this source file.
