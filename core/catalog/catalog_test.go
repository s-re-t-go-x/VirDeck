package catalog

import (
	"path/filepath"
	"testing"
)

func TestCatalogRecommendedCustomAndLocalMerge(t *testing.T) {
	tmp := t.TempDir()
	store, err := NewStore(filepath.Join(tmp, "images_catalog.json"), filepath.Join(tmp, "sdk"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// Defaults plus any locally installed images are merged without duplicates.
	installed := []string{
		"system-images;android-34;google_apis_playstore;x86_64",
		"system-images;android-37.0;google_apis_playstore_ps16k;x86_64",
	}
	list := store.List(installed)
	if len(list) != 5 {
		t.Fatalf("expected 4 presets + 1 local = 5 items, got %d", len(list))
	}
	play14, _ := store.Get(installed[0], installed)
	play13, _ := store.Get("system-images;android-33;google_apis_playstore;x86_64", installed)
	if !play14.Installed || play13.Installed {
		t.Fatalf("installed flag mismatch: %+v %+v", play14, play13)
	}
	if list[4].Source != SourceLocal || !list[4].Installed {
		t.Fatalf("expected 5th item to be SourceLocal installed, got %+v", list[4])
	}

	// Add custom image and verify persistence.
	customID := "system-images;android-35;google_apis_playstore;x86_64"
	if err := store.SaveCustom(ImageItem{
		ID:   customID,
		Name: "Android 15 Custom",
		URL:  "https://example.com/x86_64-35.zip",
		SHA1: "1234567890abcdef1234567890abcdef12345678",
	}); err != nil {
		t.Fatalf("SaveCustom: %v", err)
	}

	reloaded, err := NewStore(filepath.Join(tmp, "images_catalog.json"), filepath.Join(tmp, "sdk"))
	if err != nil {
		t.Fatalf("reload NewStore: %v", err)
	}
	got, ok := reloaded.Get(customID, installed)
	if !ok || got.Source != SourceCustom || got.URL != "https://example.com/x86_64-35.zip" {
		t.Fatalf("reloaded custom item mismatch: %+v, ok=%v", got, ok)
	}

	if err := reloaded.RemoveCustom(customID); err != nil {
		t.Fatalf("RemoveCustom: %v", err)
	}
	if _, ok := reloaded.Get(customID, installed); ok {
		t.Fatalf("expected custom item to be removed")
	}
}

func TestFavoritesPersistAndSortBeforePresets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.json")
	s, _ := NewStore(path, "")
	id := "system-images;android-35;default;x86_64"
	if err := s.SaveCustom(ImageItem{ID: id, Name: "Android 15"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFavorite(id, true, "  测试账号  "); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := NewStore(path, "")
	list := reloaded.List(nil)
	if list[1].ID != id || !list[1].Favorite || list[1].Note != "测试账号" || list[2].Favorite {
		t.Fatalf("favorite lost or not first: %+v", list)
	}
	if err := reloaded.SetFavorite(id, false, ""); err != nil {
		t.Fatal(err)
	}
	again, _ := NewStore(path, "")
	item, _ := again.Get(id, nil)
	if item.Favorite || item.Note != "" {
		t.Fatalf("unfavorite not persisted: %+v", item)
	}
}

func TestCatalogHidesCachedPrereleasesAndAddsVersion(t *testing.T) {
	s, _ := NewStore("", "")
	s.data.Official = []ImageItem{
		{ID: "system-images;android-35;google_apis;x86_64", APILevel: "35", TagID: "google_apis", Name: "Android API 35 · Google APIs"},
		{ID: "system-images;android-36;google_apis;x86_64", Name: "Android 16 Beta"},
		{ID: "system-images;android-canary-20261104;google_apis;x86_64", Name: "Android API canary-20261104"},
	}
	list := s.List(nil)
	if len(list) != 5 {
		t.Fatalf("prerelease leaked: %+v", list)
	}
	item, _ := s.Get("system-images;android-35;google_apis;x86_64", nil)
	if item.Name != "Android 15 (API 35) · Google APIs" {
		t.Fatalf("version missing: %q", item.Name)
	}
}

func TestCachedBetaCannotReplaceStablePresetDownload(t *testing.T) {
	s, _ := NewStore("", "")
	id := "system-images;android-34;google_apis_playstore;x86_64"
	s.data.Official = []ImageItem{{ID: id, Name: "Android 14 Beta", URL: "https://example.com/beta.zip"}}
	item, ok := s.Get(id, nil)
	if !ok || item.URL == "https://example.com/beta.zip" {
		t.Fatalf("beta download leaked through preset: %+v", item)
	}
}

func TestDefaultFavoriteCanBeClearedPermanently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.json")
	s, err := NewStore(path, "")
	if err != nil {
		t.Fatal(err)
	}
	id := "system-images;android-30;google_apis_playstore;x86_64"
	first := s.List(nil)[0]
	if first.ID != id || !first.Favorite || first.Note != "Gmail / Facebook / WhatsApp / Instagram / Chrome 首选" {
		t.Fatalf("default favorite missing: %+v", first)
	}
	if err := s.SetFavorite(id, false, ""); err != nil {
		t.Fatal(err)
	}
	again, err := NewStore(path, "")
	if err != nil {
		t.Fatal(err)
	}
	item, _ := again.Get(id, nil)
	if item.Favorite {
		t.Fatal("default favorite resurrected after user cleared it")
	}
}
