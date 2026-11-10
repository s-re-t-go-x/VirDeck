package catalog

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"virdeck/config"
	"virdeck/core/downloader"
)

// SourceType identifies where a system image catalog entry came from.
type SourceType string

const (
	SourceRecommended SourceType = "recommended"
	SourceOfficial    SourceType = "official"
	SourceCustom      SourceType = "custom"
	SourceLocal       SourceType = "local"
)

// ImageItem describes a system image in the catalog (recommended, official XML, custom, or locally installed).
type ImageItem struct {
	Favorite    bool       `json:"favorite,omitempty"`
	Note        string     `json:"note,omitempty"`
	ID          string     `json:"id"`          // e.g. "system-images;android-34;google_apis_playstore;x86_64"
	Name        string     `json:"name"`        // e.g. "Android 14 (API 34) - Google Play 商店"
	Description string     `json:"description"` // e.g. "Gmail / WhatsApp / FB / IG / Chrome 首选"
	APILevel    string     `json:"apiLevel"`
	TagID       string     `json:"tagId"`
	ABI         string     `json:"abi"`
	Source      SourceType `json:"source"`
	URL         string     `json:"url,omitempty"`
	SHA1        string     `json:"sha1,omitempty"`
	Size        int64      `json:"size,omitempty"`
	Revision    string     `json:"revision,omitempty"`
	Installed   bool       `json:"installed"`
	InstallDir  string     `json:"installDir,omitempty"`
}

type favorite struct {
	Note string `json:"note,omitempty"`
}

type persistedCatalog struct {
	FavoritesVersion int                 `json:"favoritesVersion,omitempty"`
	Favorites        map[string]favorite `json:"favorites"`
	Official         []ImageItem         `json:"official,omitempty"`
	Custom           []ImageItem         `json:"custom,omitempty"`
}

// Store manages the three-tier system image catalog:
// 1. Configured image presets
// 2. Official Android SDK XML synced list
// 3. User custom entries and local scanned directories
type Store struct {
	mu       sync.RWMutex
	filePath string
	sdkRoot  string
	data     persistedCatalog
}

// BuiltinRecommended returns shipped image presets from config/defaults.json.
func BuiltinRecommended() []ImageItem {
	var out []ImageItem
	for _, p := range config.Values.Images {
		out = append(out, ImageItem{ID: p.ID, Name: p.Name, APILevel: p.APILevel, TagID: p.TagID, ABI: p.ABI, URL: p.URL, SHA1: p.SHA1, Source: SourceRecommended})
	}
	return out
}

// NewStore loads or initializes a catalog Store backed by filePath and scanning sdkRoot.
func NewStore(filePath, sdkRoot string) (*Store, error) {
	s := &Store{
		filePath: filePath,
		sdkRoot:  sdkRoot,
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	fresh := s.data.Favorites == nil
	previousVersion := s.data.FavoritesVersion
	// Legacy catalogs already applied version 1 (Android 11). Its absence may
	// be an explicit unfavorite, so only newer defaults are eligible to seed.
	if !fresh && previousVersion == 0 {
		previousVersion = 1
	}
	if fresh {
		s.data.Favorites = make(map[string]favorite)
	}
	nextVersion := max(1, previousVersion)
	changed := fresh || s.data.FavoritesVersion == 0
	for _, p := range config.Values.Images {
		version := max(1, p.FavoriteVersion)
		nextVersion = max(nextVersion, version)
		if !p.Favorite || (!fresh && version <= previousVersion) {
			continue
		}
		if _, exists := s.data.Favorites[p.ID]; !exists {
			s.data.Favorites[p.ID] = favorite{Note: p.Note}
			changed = true
		}
	}
	if nextVersion > s.data.FavoritesVersion {
		s.data.FavoritesVersion = nextVersion
		changed = true
	}
	if changed {
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// SetSDKRoot updates the SDK root directory scanned for locally installed images.
func (s *Store) SetSDKRoot(sdkRoot string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sdkRoot = sdkRoot
}

func (s *Store) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.filePath == "" {
		return nil
	}
	raw, err := os.ReadFile(s.filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil
	}
	return json.Unmarshal(raw, &s.data)
}

func (s *Store) saveLocked() error {
	if s.filePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.filePath), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.filePath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.filePath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// List returns the merged catalog (Recommended -> Custom -> Official -> Local-only),
// annotated with current local installation status under sdkRoot.
func (s *Store) List(installedPackages []string) []ImageItem {
	s.mu.RLock()
	defer s.mu.RUnlock()

	installedSet := make(map[string]bool, len(installedPackages))
	for _, pkg := range installedPackages {
		installedSet[pkg] = true
	}

	officialByID := make(map[string]ImageItem, len(s.data.Official))
	for _, item := range s.data.Official {
		if isPrerelease(item) {
			continue
		}
		officialByID[item.ID] = item
	}

	seen := make(map[string]bool)
	var out []ImageItem

	// 1. Recommended presets (enriched with latest URL/SHA1/Size from synced official list if available).
	for _, rec := range BuiltinRecommended() {
		if off, ok := officialByID[rec.ID]; ok {
			if off.URL != "" {
				rec.URL = off.URL
			}
			if off.SHA1 != "" {
				rec.SHA1 = off.SHA1
			}
			if off.Size > 0 {
				rec.Size = off.Size
			}
			if off.Revision != "" {
				rec.Revision = off.Revision
			}
		}
		rec.Installed = installedSet[rec.ID]
		rec.InstallDir = s.imageDirLocked(rec.ID)
		out = append(out, rec)
		seen[rec.ID] = true
	}

	// 2. User custom items.
	for _, cust := range s.data.Custom {
		c := cust
		c.Source = SourceCustom
		c.Installed = installedSet[c.ID]
		c.InstallDir = s.imageDirLocked(c.ID)
		if seen[c.ID] {
			// Override existing entry if user customized the same package ID.
			for i := range out {
				if out[i].ID == c.ID {
					out[i] = c
					break
				}
			}
			continue
		}
		out = append(out, c)
		seen[c.ID] = true
	}

	// 3. Official synced items.
	for _, off := range s.data.Official {
		if seen[off.ID] {
			continue
		}
		o := off
		o.Source = SourceOfficial
		o.Installed = installedSet[o.ID]
		o.InstallDir = s.imageDirLocked(o.ID)
		out = append(out, o)
		seen[o.ID] = true
	}

	// 4. Any extra locally installed images discovered on disk (e.g. from existing Android Studio SDK).
	for _, pkg := range installedPackages {
		if seen[pkg] {
			continue
		}
		api, tag, abi := SplitPackageID(pkg)
		out = append(out, ImageItem{
			ID:          pkg,
			Name:        fmt.Sprintf("本地镜像 (%s · %s)", strings.TrimPrefix(api, "android-"), tag),
			Description: "本地 SDK 目录已安装镜像",
			APILevel:    strings.TrimPrefix(api, "android-"),
			TagID:       tag,
			ABI:         abi,
			Source:      SourceLocal,
			Installed:   true,
			InstallDir:  s.imageDirLocked(pkg),
		})
		seen[pkg] = true
	}

	filtered := out[:0]
	for _, item := range out {
		if isPrerelease(item) {
			continue
		}
		if item.Source == SourceOfficial {
			item.Name = officialImageName(item.APILevel, item.TagID)
		}
		pref, ok := s.data.Favorites[item.ID]
		item.Favorite, item.Note = ok, pref.Note
		filtered = append(filtered, item)
	}
	slices.SortStableFunc(filtered, func(a, b ImageItem) int {
		if a.Favorite == b.Favorite {
			return 0
		}
		if a.Favorite {
			return -1
		}
		return 1
	})
	return filtered
}

// Get returns a single catalog item by package ID.
func (s *Store) Get(id string, installedPackages []string) (ImageItem, bool) {
	for _, it := range s.List(installedPackages) {
		if it.ID == id {
			return it, true
		}
	}
	return ImageItem{}, false
}

// SaveCustom adds or updates a user-defined custom system image entry.
func (s *Store) SaveCustom(item ImageItem) error {
	item.ID = strings.TrimSpace(item.ID)
	if item.ID == "" {
		return errors.New("系统镜像包 ID 不能为空（例如 system-images;android-34;google_apis_playstore;x86_64）")
	}
	api, tag, abi := SplitPackageID(item.ID)
	if api == "" || tag == "" || abi == "" {
		return errors.New("镜像包 ID 格式应为 system-images;android-<API>;<tag>;<abi>")
	}
	item.APILevel = strings.TrimPrefix(api, "android-")
	item.TagID = tag
	item.ABI = abi
	item.Source = SourceCustom
	if strings.TrimSpace(item.Name) == "" {
		item.Name = fmt.Sprintf("自定义 Android %s (%s)", item.APILevel, item.TagID)
	}
	item.URL = strings.TrimSpace(item.URL)
	item.SHA1 = strings.TrimSpace(item.SHA1)

	s.mu.Lock()
	defer s.mu.Unlock()
	replaced := false
	for i := range s.data.Custom {
		if s.data.Custom[i].ID == item.ID {
			s.data.Custom[i] = item
			replaced = true
			break
		}
	}
	if !replaced {
		s.data.Custom = append(s.data.Custom, item)
	}
	return s.saveLocked()
}

// RemoveCustom removes a user-defined custom catalog entry by ID.
func (s *Store) RemoveCustom(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, it := range s.data.Custom {
		if it.ID == id {
			s.data.Custom = slices.Delete(s.data.Custom, i, i+1)
			return s.saveLocked()
		}
	}
	return nil
}

// SyncFromOfficial fetches Google's official sys-img2-3.xml manifests and caches x86_64 images (API 30+).
func (s *Store) SyncFromOfficial(ctx context.Context, dl *downloader.Client) (int, error) {
	urls := []string{
		downloader.GoogleSysImgPlayStoreXMLURL,
		downloader.GoogleSysImgGoogleAPIsXMLURL,
		downloader.GoogleSysImgDefaultXMLURL,
	}

	var fetched []ImageItem
	var errs []error
	seen := make(map[string]bool)

	for _, u := range urls {
		pkgs, err := dl.FetchRepositoryPackages(ctx, u)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, p := range pkgs {
			api, tag, abi := SplitPackageID(p.PackagePath)
			if abi != "x86_64" || api == "" || tag == "" || downloader.IsPrerelease(p.PackagePath+" "+p.DisplayName) {
				continue
			}
			if seen[p.PackagePath] {
				continue
			}
			seen[p.PackagePath] = true
			disp := p.DisplayName
			if disp == "" {
				disp = fmt.Sprintf("Android API %s (%s)", strings.TrimPrefix(api, "android-"), tag)
			}
			fetched = append(fetched, ImageItem{
				ID:          p.PackagePath,
				Name:        officialImageName(strings.TrimPrefix(api, "android-"), tag),
				Description: disp,
				APILevel:    strings.TrimPrefix(api, "android-"),
				TagID:       tag,
				ABI:         abi,
				Source:      SourceOfficial,
				URL:         p.URL,
				SHA1:        p.SHA1,
				Size:        p.Size,
				Revision:    p.Revision,
			})
		}
	}

	if len(fetched) == 0 && len(errs) > 0 {
		return 0, errors.Join(errs...)
	}

	slices.SortFunc(fetched, func(a, b ImageItem) int {
		if a.APILevel != b.APILevel {
			return strings.Compare(b.APILevel, a.APILevel) // newest API first
		}
		return strings.Compare(a.ID, b.ID)
	})

	s.mu.Lock()
	s.data.Official = fetched
	err := s.saveLocked()
	s.mu.Unlock()
	return len(fetched), err
}

func (s *Store) imageDirLocked(pkgID string) string {
	if s.sdkRoot == "" {
		return ""
	}
	api, tag, abi := SplitPackageID(pkgID)
	if api == "" || tag == "" || abi == "" {
		return ""
	}
	return filepath.Join(s.sdkRoot, "system-images", api, tag, abi)
}

// SplitPackageID splits "system-images;android-34;google_apis_playstore;x86_64" into ("android-34", "google_apis_playstore", "x86_64").
func SplitPackageID(pkgID string) (apiDir, tag, abi string) {
	parts := strings.Split(strings.TrimSpace(pkgID), ";")
	if len(parts) != 4 || parts[0] != "system-images" {
		return "", "", ""
	}
	return parts[1], parts[2], parts[3]
}

func tagDisplayName(tag, fallback string) string {
	switch tag {
	case "google_apis_playstore":
		return "Google Play 商店"
	case "google_apis":
		return "Google APIs"
	case "default":
		return "AOSP 纯净版"
	}
	if fallback != "" {
		return fallback
	}
	return tag
}

// API version names: https://developer.android.com/tools/releases/platforms
//
//go:embed android_versions.json
var versionFiles embed.FS

var androidVersions = func() map[string]string {
	raw, _ := versionFiles.ReadFile("android_versions.json")
	var versions map[string]string
	_ = json.Unmarshal(raw, &versions)
	return versions
}()

func officialImageName(api, tag string) string {
	if version := androidVersions[api]; version != "" {
		return fmt.Sprintf("Android %s (API %s) · %s", version, api, tagDisplayName(tag, ""))
	}
	return fmt.Sprintf("Android API %s · %s", api, tagDisplayName(tag, ""))
}

func isPrerelease(item ImageItem) bool {
	api, _, _ := SplitPackageID(item.ID)
	level := strings.TrimPrefix(api, "android-")
	if level != "" {
		if _, err := strconv.ParseFloat(level, 64); err != nil {
			return true
		}
	}
	return downloader.IsPrerelease(item.ID + " " + item.Name + " " + item.Description)
}

// SetFavorite stores personal metadata separately so official sync cannot replace it.
func (s *Store) SetFavorite(id string, selected bool, note string) error {
	api, tag, abi := SplitPackageID(id)
	if api == "" || tag == "" || abi == "" {
		return errors.New("无效的镜像包 ID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.Favorites == nil {
		s.data.Favorites = make(map[string]favorite)
	}
	previous, existed := s.data.Favorites[id]
	if selected {
		s.data.Favorites[id] = favorite{Note: strings.TrimSpace(note)}
	} else {
		delete(s.data.Favorites, id)
	}
	if err := s.saveLocked(); err != nil {
		if existed {
			s.data.Favorites[id] = previous
		} else {
			delete(s.data.Favorites, id)
		}
		return err
	}
	return nil
}
