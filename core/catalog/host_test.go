package catalog

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"virdeck/core/downloader"
)

func TestArmCatalogHidesIncompatibleOfficialImages(t *testing.T) {
	s, err := NewStoreForHost("", "", "darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	s.data.Official = []ImageItem{{ID: "system-images;android-34;google_apis;arm64-v8a", ABI: "arm64-v8a", APILevel: "34", TagID: "google_apis"}, {ID: "system-images;android-34;google_apis;x86_64", ABI: "x86_64", APILevel: "34", TagID: "google_apis"}}
	items := s.List(nil)
	found := false
	for _, it := range items {
		if it.ABI != "arm64-v8a" {
			t.Fatalf("incompatible image: %+v", it)
		}
		if it.ID == "system-images;android-34;google_apis;arm64-v8a" {
			found = true
		}
	}
	if !found {
		t.Fatal("official ARM image hidden")
	}
	if got, ok := s.Get("system-images;android-30;google_apis_playstore;arm64-v8a", nil); !ok || !got.Favorite {
		t.Fatalf("ARM default favorite missing: %+v", got)
	}
}

func TestArmOfficialSyncUsesCatalogHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<repository><remotePackage path="system-images;android-30;google_apis_playstore;arm64-v8a"><archives><archive><host-os>macosx</host-os><complete><url>arm-darwin.zip</url></complete></archive></archives></remotePackage><remotePackage path="system-images;android-35;google_apis;x86_64"><archives><archive><complete><url>intel.zip</url></complete></archive></archives></remotePackage></repository>`)
	}))
	defer srv.Close()
	play, apis, def := downloader.GoogleSysImgPlayStoreXMLURL, downloader.GoogleSysImgGoogleAPIsXMLURL, downloader.GoogleSysImgDefaultXMLURL
	t.Cleanup(func() {
		downloader.GoogleSysImgPlayStoreXMLURL = play
		downloader.GoogleSysImgGoogleAPIsXMLURL = apis
		downloader.GoogleSysImgDefaultXMLURL = def
	})
	downloader.GoogleSysImgPlayStoreXMLURL = srv.URL + "/play.xml"
	downloader.GoogleSysImgGoogleAPIsXMLURL = srv.URL + "/apis.xml"
	downloader.GoogleSysImgDefaultXMLURL = srv.URL + "/default.xml"
	s, err := NewStoreForHost("", "", "darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.SyncFromOfficial(context.Background(), downloader.New(t.TempDir(), ""))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("got %d compatible official images, want 1", n)
	}
	item, ok := s.Get("system-images;android-30;google_apis_playstore;arm64-v8a", nil)
	if !ok || item.URL != srv.URL+"/arm-darwin.zip" {
		t.Fatalf("ARM preset not enriched: %+v", item)
	}
}
