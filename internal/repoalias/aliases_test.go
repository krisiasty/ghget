package repoalias

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestParseAcceptsRunsOfWhitespace(t *testing.T) {
	input := "  # aliases\n\nFD\tsharkdp/fd\nkubens  ahmetb/kubectx\t kubens\nkubectl kubernetes/kubectl kubectl kubernetes\nrg BurntSushi/ripgrep\n"
	entries, err := Parse(strings.NewReader(input), "test aliases")
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		{Alias: "fd", Repository: "sharkdp/fd"},
		{Alias: "kubectl", Repository: "kubernetes/kubectl", AssetHint: "kubectl", Backend: "kubernetes"},
		{Alias: "kubens", Repository: "ahmetb/kubectx", AssetHint: "kubens"},
		{Alias: "rg", Repository: "BurntSushi/ripgrep"},
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
		{name: "missing repository", input: "fd\n", want: "expected alias, OWNER/REPO"},
		{name: "extra field", input: "fd sharkdp/fd hint backend extra\n", want: "found 5 fields"},
		{name: "alias path", input: "tools/fd sharkdp/fd\n", want: "invalid alias"},
		{name: "invalid repository", input: "fd sharkdp\n", want: "invalid repository"},
		{name: "invalid asset hint", input: "fd sharkdp/fd tools/fd\n", want: "invalid asset hint"},
		{name: "invalid backend", input: "fd sharkdp/fd fd sources/github\n", want: "invalid backend"},
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
		entry, found, err := Lookup(alias)
		if err != nil {
			t.Fatal(err)
		}
		if !found || entry.Repository != "sharkdp/fd" {
			t.Fatalf("Lookup(%q) = %#v, %v, want repository sharkdp/fd, true", alias, entry, found)
		}
	}
}

func TestLookupReturnsAssetHint(t *testing.T) {
	for _, alias := range []string{"kubens", "logcli"} {
		entry, found, err := Lookup(alias)
		if err != nil {
			t.Fatal(err)
		}
		if !found || entry.AssetHint != alias {
			t.Fatalf("Lookup(%q) = %#v, %v, want asset hint %q, true", alias, entry, found, alias)
		}
	}
}

func TestLookupReturnsBackend(t *testing.T) {
	for _, alias := range []string{"kubeadm", "kubectl"} {
		entry, found, err := Lookup(alias)
		if err != nil {
			t.Fatal(err)
		}
		if !found || entry.AssetHint != alias || entry.Backend != "kubernetes" {
			t.Fatalf("Lookup(%q) = %#v, %v, want Kubernetes backend for %q", alias, entry, found, alias)
		}
	}
}

func TestPopularDevOpsAliases(t *testing.T) {
	tests := []Entry{
		{Alias: "calicoctl", Repository: "projectcalico/calico"},
		{Alias: "dra", Repository: "devmatteini/dra"},
		{Alias: "helm", Repository: "helm/helm"},
		{Alias: "hl", Repository: "pamburus/hl"},
		{Alias: "hwatch", Repository: "blacknon/hwatch"},
		{Alias: "jq", Repository: "jqlang/jq"},
		{Alias: "k9s", Repository: "derailed/k9s"},
		{Alias: "kubeadm", Repository: "kubernetes/kubernetes"},
		{Alias: "kubectl", Repository: "kubernetes/kubectl"},
		{Alias: "openbao", Repository: "openbao/openbao"},
		{Alias: "opentofu", Repository: "opentofu/opentofu"},
		{Alias: "rclone", Repository: "rclone/rclone"},
		{Alias: "terraform", Repository: "hashicorp/terraform", AssetHint: "terraform", Backend: "hashicorp"},
		{Alias: "vault", Repository: "hashicorp/vault", AssetHint: "vault", Backend: "hashicorp"},
	}
	for _, test := range tests {
		entry, found, err := Lookup(test.Alias)
		if err != nil {
			t.Fatal(err)
		}
		if !found || entry.Repository != test.Repository {
			t.Fatalf("Lookup(%q) = %#v, %v, want repository %q, true", test.Alias, entry, found, test.Repository)
		}
		if test.Backend != "" && (entry.AssetHint != test.AssetHint || entry.Backend != test.Backend) {
			t.Fatalf("Lookup(%q) = %#v, want asset hint %q and backend %q", test.Alias, entry, test.AssetHint, test.Backend)
		}
	}
}

func TestUnsupportedToolsAreNotAliased(t *testing.T) {
	for _, alias := range []string{
		"aws", "awscli", "az", "azure-cli", "hf", "pulumi",
	} {
		entry, found, err := Lookup(alias)
		if err != nil {
			t.Fatal(err)
		}
		if found {
			t.Fatalf("Lookup(%q) = %#v, true, want no built-in alias", alias, entry)
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
