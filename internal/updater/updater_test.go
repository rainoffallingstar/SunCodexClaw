package updater

import "testing"

func TestFindReleaseAsset(t *testing.T) {
	assets := []releaseAsset{
		{Name: "v2026.03.20-linux-amd64.tar.gz"},
		{Name: "v2026.03.20-darwin-arm64.tar.gz"},
	}
	asset, err := findReleaseAsset(assets, "darwin", "arm64")
	if err != nil {
		t.Fatalf("findReleaseAsset() error = %v", err)
	}
	if asset.Name != "v2026.03.20-darwin-arm64.tar.gz" {
		t.Fatalf("findReleaseAsset() = %q, want darwin arm64 asset", asset.Name)
	}
}

func TestParseSHA256File(t *testing.T) {
	raw := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef  release.tar.gz\n"
	got := parseSHA256File(raw)
	want := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if got != want {
		t.Fatalf("parseSHA256File() = %q, want %q", got, want)
	}
}
