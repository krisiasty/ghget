package repoalias

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestParseAcceptsRunsOfWhitespace(t *testing.T) {
	input := "  # aliases\n\nFD\tsharkdp/fd\nrg       BurntSushi/ripgrep\nuv\t  astral-sh/uv\n"
	entries, err := Parse(strings.NewReader(input), "test aliases")
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		{Alias: "fd", Repository: "sharkdp/fd"},
		{Alias: "rg", Repository: "BurntSushi/ripgrep"},
		{Alias: "uv", Repository: "astral-sh/uv"},
	}
	if !reflect.DeepEqual(entries, want) {
		t.Fatalf("Parse() = %#v, want %#v", entries, want)
	}
}

func TestParseRejectsInvalidEntries(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "missing repository", input: "fd\n", want: "expected alias and OWNER/REPO"},
		{name: "extra field", input: "fd sharkdp/fd extra\n", want: "found 3 fields"},
		{name: "alias path", input: "tools/fd sharkdp/fd\n", want: "invalid alias"},
		{name: "invalid repository", input: "fd sharkdp\n", want: "invalid repository"},
		{name: "case-insensitive duplicate", input: "fd sharkdp/fd\nFD fork/fd\n", want: "duplicate alias"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(test.input), "aliases.tsv")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Parse() error = %v, want it to contain %q", err, test.want)
			}
			if !strings.Contains(err.Error(), "aliases.tsv:") {
				t.Fatalf("Parse() error = %v, want source and line number", err)
			}
		})
	}
}

func TestLookupIsCaseInsensitive(t *testing.T) {
	for _, alias := range []string{"fd", "FD", "Fd"} {
		repository, found, err := Lookup(alias)
		if err != nil {
			t.Fatal(err)
		}
		if !found || repository != "sharkdp/fd" {
			t.Fatalf("Lookup(%q) = %q, %v, want sharkdp/fd, true", alias, repository, found)
		}
	}
}

func TestPopularDevOpsAliases(t *testing.T) {
	tests := []Entry{
		{Alias: "azure-cli", Repository: "Azure/azure-cli"},
		{Alias: "helm", Repository: "helm/helm"},
		{Alias: "hf", Repository: "huggingface/huggingface_hub"},
		{Alias: "hwatch", Repository: "blacknon/hwatch"},
		{Alias: "jq", Repository: "jqlang/jq"},
		{Alias: "k9s", Repository: "derailed/k9s"},
		{Alias: "kubeadm", Repository: "kubernetes/kubernetes"},
		{Alias: "kubectl", Repository: "kubernetes/kubectl"},
		{Alias: "openbao", Repository: "openbao/openbao"},
		{Alias: "opentofu", Repository: "opentofu/opentofu"},
		{Alias: "rclone", Repository: "rclone/rclone"},
		{Alias: "terraform", Repository: "hashicorp/terraform"},
		{Alias: "vault", Repository: "hashicorp/vault"},
	}
	for _, test := range tests {
		repository, found, err := Lookup(test.Alias)
		if err != nil {
			t.Fatal(err)
		}
		if !found || repository != test.Repository {
			t.Fatalf("Lookup(%q) = %q, %v, want %q, true", test.Alias, repository, found, test.Repository)
		}
	}
}

func TestInstallerOnlyToolsAreNotAliased(t *testing.T) {
	for _, alias := range []string{"aws", "awscli"} {
		repository, found, err := Lookup(alias)
		if err != nil {
			t.Fatal(err)
		}
		if found {
			t.Fatalf("Lookup(%q) = %q, true, want no built-in alias", alias, repository)
		}
	}
}

func TestEmbeddedAliasesMatchSource(t *testing.T) {
	source, err := os.Open("../../registry/aliases.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = source.Close() }()
	want, err := Parse(source, "registry/aliases.tsv")
	if err != nil {
		t.Fatal(err)
	}
	got, err := loadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("embedded aliases = %#v, want %#v; run go generate ./internal/repoalias", got, want)
	}
}
