package autoselect

import "testing"

func TestProgramName(t *testing.T) {
	tests := []struct {
		asset string
		want  string
	}{
		{"kind-linux-amd64", "kind"},
		{"jq-linux-amd64", "jq"},
		{"docker-compose-linux-x86_64", "docker-compose"},
		{"docker-compose-darwin-aarch64", "docker-compose"},
		{"mise-v2026.8.13-linux-x64", "mise"},
		{"jq-windows-amd64.exe", "jq.exe"},
		{"mise-v2026.8.13-windows-x64.exe", "mise.exe"},
		{"docker-compose-windows-x86_64.exe", "docker-compose.exe"},
		// Nothing recognisable to strip.
		{"fzf", "fzf"},
		// Stripping everything would leave no name at all.
		{"linux-amd64", "linux-amd64"},
		{"win64.exe", "win64.exe"},
	}
	for _, tt := range tests {
		t.Run(tt.asset, func(t *testing.T) {
			if got := ProgramName(tt.asset); got != tt.want {
				t.Fatalf("ProgramName(%q) = %q, want %q", tt.asset, got, tt.want)
			}
		})
	}
}
