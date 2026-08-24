package checksum

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParsePopularFormats(t *testing.T) {
	a := strings.Repeat("a", 64)
	b := strings.Repeat("b", 64)
	c := strings.Repeat("c", 64)
	d := strings.Repeat("d", 64)
	input := fmt.Sprintf("%s  first.zip\nsecond.tgz %s\nSHA256 (third.tar) = %s\n%s\n", a, b, c, d)
	got, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{{"first.zip", a}, {"second.tgz", b}, {"third.tar", c}, {"", d}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse() = %#v, want %#v", got, want)
	}
}

func TestVerifyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asset.bin")
	content := []byte("release content")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(content))
	if err := VerifyFile(path, "asset.bin", []Entry{{Filename: "asset.bin", Digest: digest}}); err != nil {
		t.Fatal(err)
	}
	if err := VerifyFile(path, "asset.bin", []Entry{{Filename: "asset.bin", Digest: strings.Repeat("0", 64)}}); err == nil {
		t.Fatal("checksum mismatch unexpectedly accepted")
	}
}

func TestIsChecksumAsset(t *testing.T) {
	for _, name := range []string{"checksums.txt", "SHA256SUMS", "tool.tar.gz.sha256"} {
		if !IsChecksumAsset(name) {
			t.Errorf("%q was not recognized", name)
		}
	}
	if IsChecksumAsset("tool.tar.gz") {
		t.Fatal("regular archive was recognized as checksum file")
	}
	if IsChecksumAsset("checksums.txt.sig") {
		t.Fatal("checksum signature was recognized as checksum file")
	}
	if got := TargetFromSidecar("tool.tar.gz.sha256"); got != "tool.tar.gz" {
		t.Fatalf("TargetFromSidecar() = %q", got)
	}
}
