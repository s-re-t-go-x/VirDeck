package catalog

import (
	"os"
	"path/filepath"
	"testing"
	"virdeck/config"
)

func TestCatalogRecommendedCustomAndLocalMerge(t *testing.T) {
	tmp := t.TempDir()
	store, err := windowsStore(filepath.Join(tmp, "images_catalog.json"), filepath.Join(tmp, "sdk"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// Defaults plus any locally installed images are merged without duplicates.
	installed := []string{
		"system-images;android-34;google_apis_playstore;x86_64",
		"system-images;android-37.0;google_apis_playstore_ps16k;x86_64",
	}
	list := store.List(installed)
	if len(list) != len(windowsRecommended())+1 {
		t.Fatalf("expected presets plus one local image, got %d", len(list))
	}
	play14, _ := store.Get(installed[0], installed)
	play13, _ := store.Get("system-images;android-33;google_apis_playstore;x86_64", installed)
	if !play14.Installed || play13.Installed {
		t.Fatalf("installed flag mismatch: %+v %+v", play14, play13)
	}
	if last := list[len(list)-1]; last.Source != SourceLocal || !last.Installed {
		t.Fatalf("expected last item to be SourceLocal installed, got %+v", last)
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

	reloaded, err := windowsStore(filepath.Join(tmp, "images_catalog.json"), filepath.Join(tmp, "sdk"))
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
	s, _ := windowsStore(path, "")
	id := "system-images;android-35;default;x86_64"
	if err := s.SaveCustom(ImageItem{ID: id, Name: "Android 15"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFavorite(id, true, "  测试账号  "); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := windowsStore(path, "")
	list := reloaded.List(nil)
	found := false
	nonFavoriteSeen := false
	for _, item := range list {
		if !item.Favorite {
			nonFavoriteSeen = true
		} else if nonFavoriteSeen {
			t.Fatal("favorite sorted after non-favorite")
		}
		if item.ID == id {
			found = item.Favorite && item.Note == "测试账号"
		}
	}
	if !found {
		t.Fatalf("favorite lost: %+v", list)
	}
	if err := reloaded.SetFavorite(id, false, ""); err != nil {
		t.Fatal(err)
	}
	again, _ := windowsStore(path, "")
	item, _ := again.Get(id, nil)
	if item.Favorite || item.Note != "" {
		t.Fatalf("unfavorite not persisted: %+v", item)
	}
}

func TestCatalogHidesCachedPrereleasesAndAddsVersion(t *testing.T) {
	s, _ := windowsStore("", "")
	s.data.Official = []ImageItem{
		{ID: "system-images;android-35;google_apis;x86_64", APILevel: "35", TagID: "google_apis", Name: "Android API 35 · Google APIs"},
		{ID: "system-images;android-36;google_apis;x86_64", Name: "Android 16 Beta"},
		{ID: "system-images;android-canary-20261104;google_apis;x86_64", Name: "Android API canary-20261104"},
	}
	list := s.List(nil)
	if len(list) != len(windowsRecommended())+1 {
		t.Fatalf("prerelease leaked: %+v", list)
	}
	item, _ := s.Get("system-images;android-35;google_apis;x86_64", nil)
	if item.Name != "Android 15 (API 35) · Google APIs" {
		t.Fatalf("version missing: %q", item.Name)
	}
}

func TestCachedBetaCannotReplaceStablePresetDownload(t *testing.T) {
	s, _ := windowsStore("", "")
	id := "system-images;android-34;google_apis_playstore;x86_64"
	s.data.Official = []ImageItem{{ID: id, Name: "Android 14 Beta", URL: "https://example.com/beta.zip"}}
	item, ok := s.Get(id, nil)
	if !ok || item.URL == "https://example.com/beta.zip" {
		t.Fatalf("beta download leaked through preset: %+v", item)
	}
}

func TestDefaultFavoriteCanBeClearedPermanently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.json")
	s, err := windowsStore(path, "")
	if err != nil {
		t.Fatal(err)
	}
	id := "system-images;android-30;google_apis_playstore;x86_64"
	first := s.List(nil)[0]
	if first.ID != id || !first.Favorite || first.Note == "" {
		t.Fatalf("default favorite missing: %+v", first)
	}
	if err := s.SetFavorite(id, false, ""); err != nil {
		t.Fatal(err)
	}
	again, err := windowsStore(path, "")
	if err != nil {
		t.Fatal(err)
	}
	item, _ := again.Get(id, nil)
	if item.Favorite {
		t.Fatal("default favorite resurrected after user cleared it")
	}
}

func TestNewCatalogSeedsCompatibilityFavoritesWithVerifiedLegacyDownload(t *testing.T) {
	store, err := windowsStore(filepath.Join(t.TempDir(), "catalog.json"), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{
		"system-images;android-25;google_apis_playstore;x86",
		"system-images;android-28;google_apis_playstore;x86_64",
		"system-images;android-30;google_apis_playstore;x86_64",
	} {
		item, ok := store.Get(id, nil)
		if !ok || !item.Favorite || item.Note == "" {
			t.Fatalf("compatibility favorite missing: %s %+v", id, item)
		}
		if item.ABI == "x86" && (item.URL == "" || len(item.SHA1) != 40) {
			t.Fatalf("legacy image cannot download with checksum: %+v", item)
		}
	}
}

func TestLegacyCatalogAddsNewFavoritesWithoutReplacingNotes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.json")
	raw := `{"favorites":{"system-images;android-30;google_apis_playstore;x86_64":{"note":"首选"},"system-images;android-33;google_apis_playstore;x86_64":{"note":"我自己的备注"},"system-images;android-35;default;x86_64":{"note":"自定义收藏"}}}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := windowsStore(path, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"system-images;android-25;google_apis_playstore;x86", "system-images;android-28;google_apis_playstore;x86_64"} {
		item, _ := s.Get(id, nil)
		if !item.Favorite || item.Note == "" {
			t.Fatalf("new default missing: %+v", item)
		}
	}
	for id, note := range map[string]string{"system-images;android-30;google_apis_playstore;x86_64": "首选", "system-images;android-33;google_apis_playstore;x86_64": "我自己的备注", "system-images;android-35;default;x86_64": "自定义收藏"} {
		if got := s.data.Favorites[id].Note; got != note {
			t.Fatalf("existing note overwritten for %s: %q", id, got)
		}
	}
}
func TestLegacyUnfavoriteAndPostMigrationEditsSurviveReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(path, []byte(`{"favorites":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := windowsStore(path, "")
	if err != nil {
		t.Fatal(err)
	}
	oldID := "system-images;android-30;google_apis_playstore;x86_64"
	newID := "system-images;android-25;google_apis_playstore;x86"
	noteID := "system-images;android-28;google_apis_playstore;x86_64"
	if old, _ := s.Get(oldID, nil); old.Favorite {
		t.Fatal("legacy cleared default resurrected")
	}
	if added, _ := s.Get(newID, nil); !added.Favorite {
		t.Fatal("new default not migrated")
	}
	if err := s.SetFavorite(newID, false, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFavorite(noteID, true, ""); err != nil {
		t.Fatal(err)
	}
	again, err := windowsStore(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if item, _ := again.Get(newID, nil); item.Favorite {
		t.Fatal("new default resurrected after user cleared it")
	}
	if item, _ := again.Get(noteID, nil); !item.Favorite || item.Note != "" {
		t.Fatal("user-cleared note overwritten on reload")
	}
	if item, _ := again.Get(oldID, nil); item.Favorite {
		t.Fatal("legacy default resurrected on reload")
	}
}

// Existing fixtures exercise the shipped Windows presets and their migrations
// on every test host; ARM behavior has separate explicit-host fixtures.
func windowsStore(filePath, sdkRoot string) (*Store, error) {
	return NewStoreForHost(filePath, sdkRoot, "windows", "amd64")
}
func windowsRecommended() []ImageItem {
	d, _ := config.ForHost("windows", "amd64")
	return recommended(d)
}
