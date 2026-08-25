package archive

import "testing"

func TestSupported(t *testing.T) {
	tests := []struct {
		asset string
		want  bool
	}{
		{"tool-linux-amd64.tar.gz", true},
		{"tool.tgz", true},
		{"tool.tar", true},
		{"tool.zip", true},
		{"tool.gz", true},
		{"TOOL.TAR.GZ", true},
		{"tool-linux-amd64", false},
		{"tool-windows-amd64.exe", false},
		{"tool-linux-amd64.tar.zst", false},
		{"tool-linux-amd64.tar.xz", false},
		{"tool.7z", false},
	}
	for _, tt := range tests {
		t.Run(tt.asset, func(t *testing.T) {
			if got := Supported(tt.asset); got != tt.want {
				t.Fatalf("Supported(%q) = %v, want %v", tt.asset, got, tt.want)
			}
		})
	}
}
