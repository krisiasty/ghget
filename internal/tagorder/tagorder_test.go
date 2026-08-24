package tagorder

import (
	"reflect"
	"testing"
)

func TestSortSemanticVersionsNewestFirst(t *testing.T) {
	tags := []string{
		"v1.0.0-alpha",
		"v2.0.0",
		"1.10.0",
		"v1.10.0-rc.10",
		"v1.10.0-rc.2",
		"v1.10.0",
		"v1.2.0",
	}
	want := []string{
		"latest",
		"v2.0.0",
		"1.10.0",
		"v1.10.0",
		"v1.10.0-rc.10",
		"v1.10.0-rc.2",
		"v1.2.0",
		"v1.0.0-alpha",
	}
	if got := Sort(tags); !reflect.DeepEqual(got, want) {
		t.Fatalf("Sort() = %v, want %v", got, want)
	}
}

func TestSortFallsBackToAlphabeticOrder(t *testing.T) {
	tags := []string{"v2.0.0", "nightly", "v1.0.0", "release-3"}
	want := []string{"latest", "nightly", "release-3", "v1.0.0", "v2.0.0"}
	if got := Sort(tags); !reflect.DeepEqual(got, want) {
		t.Fatalf("Sort() = %v, want %v", got, want)
	}
}

func TestSortIncludesLatestExactlyOnce(t *testing.T) {
	if got, want := Sort([]string{"v1.0.0", "latest"}), []string{"latest", "v1.0.0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Sort() = %v, want %v", got, want)
	}
	if got, want := Sort(nil), []string{"latest"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Sort(nil) = %v, want %v", got, want)
	}
}

func TestParseRejectsNonSemVer(t *testing.T) {
	for _, tag := range []string{"1", "1.2", "v", "v1.02.3", "v1.2.3-01", "V1.2.3", "release-1.2.3"} {
		if _, ok := parse(tag); ok {
			t.Errorf("parse(%q) unexpectedly succeeded", tag)
		}
	}
}

func TestVariants(t *testing.T) {
	tests := []struct {
		tag  string
		want []string
	}{
		{tag: "v1.3.0", want: []string{"v1.3.0", "1.3.0"}},
		{tag: "1.3.0", want: []string{"1.3.0", "v1.3.0"}},
		{tag: "v2.0.0-rc.1+build.4", want: []string{"v2.0.0-rc.1+build.4", "2.0.0-rc.1+build.4"}},
		{tag: "nightly", want: []string{"nightly"}},
	}
	for _, test := range tests {
		if got := Variants(test.tag); !reflect.DeepEqual(got, test.want) {
			t.Errorf("Variants(%q) = %v, want %v", test.tag, got, test.want)
		}
	}
}
