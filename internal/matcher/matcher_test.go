package matcher

import (
	"reflect"
	"testing"
)

func TestSelect(t *testing.T) {
	names := []string{"tool-linux-amd64.tar.gz", "tool-darwin-arm64.zip", "checksums.txt"}
	tests := []struct {
		name    string
		pattern string
		mode    Mode
		want    []string
	}{
		{"exact", "checksums.txt", Exact, []string{"checksums.txt"}},
		{"glob", "tool-*.zip", Glob, []string{"tool-darwin-arm64.zip"}},
		{"regex", `^tool-(linux|darwin)-`, Regex, []string{"tool-linux-amd64.tar.gz", "tool-darwin-arm64.zip"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Select(names, tt.pattern, tt.mode)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Select() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSelectRejectsInvalidPatterns(t *testing.T) {
	if _, err := Select(nil, "[", Glob); err == nil {
		t.Fatal("invalid glob unexpectedly accepted")
	}
	if _, err := Select(nil, "(", Regex); err == nil {
		t.Fatal("invalid regex unexpectedly accepted")
	}
}
