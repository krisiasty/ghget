# Repository aliases

This directory holds the source registry for ghget's built-in repository
aliases. Each non-comment line in `aliases.tsv` contains an alias followed by
an `OWNER/REPO` value.

Any non-empty run of whitespace may separate the two fields:

```text
fd      sharkdp/fd
rg      BurntSushi/ripgrep
```

Aliases are case-insensitive and must be unique. Blank lines and lines whose first non-whitespace character is `#` are ignored.

After editing the registry, regenerate its compressed embedded form:

```sh
go generate ./internal/repoalias
```

Tests verify that the embedded registry matches this source file.
