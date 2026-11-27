package apk

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"virdeck/core/catalog"
)

func encodeTestAXML(stringsList []string, utf8 bool, pkgAttrVal string) []byte {
	// Ensure stringsList contains "manifest", "package", and pkgAttrVal.
	var poolBuf bytes.Buffer
	offsets := make([]uint32, len(stringsList))
	for i, s := range stringsList {
		offsets[i] = uint32(poolBuf.Len())
		if utf8 {
			u16Len := len(utf16.Encode([]rune(s)))
			poolBuf.WriteByte(byte(u16Len))
			poolBuf.WriteByte(byte(len(s)))
			poolBuf.WriteString(s)
			poolBuf.WriteByte(0)
		} else {
			u16 := utf16.Encode([]rune(s))
			_ = binary.Write(&poolBuf, binary.LittleEndian, uint16(len(u16)))
			for _, u := range u16 {
				_ = binary.Write(&poolBuf, binary.LittleEndian, u)
			}
			_ = binary.Write(&poolBuf, binary.LittleEndian, uint16(0))
		}
	}
	for poolBuf.Len()%4 != 0 {
		poolBuf.WriteByte(0)
	}

	poolHeaderSize := 28
	stringsStart := poolHeaderSize + len(offsets)*4
	poolChunkSize := stringsStart + poolBuf.Len()
	var poolChunk bytes.Buffer
	_ = binary.Write(&poolChunk, binary.LittleEndian, uint16(resStringPoolType))
	_ = binary.Write(&poolChunk, binary.LittleEndian, uint16(poolHeaderSize))
	_ = binary.Write(&poolChunk, binary.LittleEndian, uint32(poolChunkSize))
	_ = binary.Write(&poolChunk, binary.LittleEndian, uint32(len(stringsList)))
	_ = binary.Write(&poolChunk, binary.LittleEndian, uint32(0)) // styleCount
	var flags uint32
	if utf8 {
		flags = stringPoolUTF8Flag
	}
	_ = binary.Write(&poolChunk, binary.LittleEndian, flags)
	_ = binary.Write(&poolChunk, binary.LittleEndian, uint32(stringsStart))
	_ = binary.Write(&poolChunk, binary.LittleEndian, uint32(0)) // stylesStart
	for _, off := range offsets {
		_ = binary.Write(&poolChunk, binary.LittleEndian, off)
	}
	poolChunk.Write(poolBuf.Bytes())

	idxOf := func(target string) int32 {
		for i, s := range stringsList {
			if s == target {
				return int32(i)
			}
		}
		return -1
	}

	var elemChunk bytes.Buffer
	elemHeaderSize := 16
	attrExtSize := 20
	attrSize := 20
	elemChunkSize := elemHeaderSize + attrExtSize + attrSize
	_ = binary.Write(&elemChunk, binary.LittleEndian, uint16(resXMLStartElementType))
	_ = binary.Write(&elemChunk, binary.LittleEndian, uint16(elemHeaderSize))
	_ = binary.Write(&elemChunk, binary.LittleEndian, uint32(elemChunkSize))
	_ = binary.Write(&elemChunk, binary.LittleEndian, uint32(1))  // lineNumber
	_ = binary.Write(&elemChunk, binary.LittleEndian, int32(-1)) // comment
	// ResXMLTree_attrExt
	_ = binary.Write(&elemChunk, binary.LittleEndian, int32(-1))              // ns
	_ = binary.Write(&elemChunk, binary.LittleEndian, idxOf("manifest"))      // name
	_ = binary.Write(&elemChunk, binary.LittleEndian, uint16(attrExtSize))    // attributeStart
	_ = binary.Write(&elemChunk, binary.LittleEndian, uint16(attrSize))       // attributeSize
	_ = binary.Write(&elemChunk, binary.LittleEndian, uint16(1))              // attributeCount
	_ = binary.Write(&elemChunk, binary.LittleEndian, uint16(0))              // idIndex
	_ = binary.Write(&elemChunk, binary.LittleEndian, uint16(0))              // classIndex
	_ = binary.Write(&elemChunk, binary.LittleEndian, uint16(0))              // styleIndex
	// ResXMLTree_attribute (package)
	valIdx := idxOf(pkgAttrVal)
	_ = binary.Write(&elemChunk, binary.LittleEndian, int32(-1))              // ns
	_ = binary.Write(&elemChunk, binary.LittleEndian, idxOf("package"))       // name
	_ = binary.Write(&elemChunk, binary.LittleEndian, valIdx)                 // rawValue
	_ = binary.Write(&elemChunk, binary.LittleEndian, uint16(8))              // typedValue.size
	elemChunk.WriteByte(0)                                                    // res0
	elemChunk.WriteByte(typedValueStringType)                                 // dataType
	_ = binary.Write(&elemChunk, binary.LittleEndian, uint32(valIdx))         // data

	var full bytes.Buffer
	totalSize := 8 + poolChunk.Len() + elemChunk.Len()
	_ = binary.Write(&full, binary.LittleEndian, uint16(resXMLType))
	_ = binary.Write(&full, binary.LittleEndian, uint16(8))
	_ = binary.Write(&full, binary.LittleEndian, uint32(totalSize))
	full.Write(poolChunk.Bytes())
	full.Write(elemChunk.Bytes())
	return full.Bytes()
}

func buildTestAPK(t *testing.T, pkg string, utf8Pool bool, abis []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.apk")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	if pkg != "" {
		w, err := zw.Create("AndroidManifest.xml")
		if err != nil {
			t.Fatal(err)
		}
		axml := encodeTestAXML([]string{"manifest", "package", pkg}, utf8Pool, pkg)
		if _, err := w.Write(axml); err != nil {
			t.Fatal(err)
		}
	}
	for _, abi := range abis {
		w, err := zw.Create("lib/" + abi + "/libdemo.so")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("ELF")); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInspectUTF16AndUTF8AndABIs(t *testing.T) {
	p1 := buildTestAPK(t, "com.example.demo", false, []string{"armeabi-v7a", "arm64-v8a", "arm64-v8a"})
	m1, err := Inspect(p1)
	if err != nil {
		t.Fatalf("Inspect UTF-16 failed: %v", err)
	}
	if m1.Package != "com.example.demo" {
		t.Fatalf("got package %q", m1.Package)
	}
	if len(m1.ABIs) != 2 || m1.ABIs[0] != "arm64-v8a" || m1.ABIs[1] != "armeabi-v7a" {
		t.Fatalf("unexpected ABIs: %v", m1.ABIs)
	}

	p2 := buildTestAPK(t, "org.virdeck.pure", true, nil)
	m2, err := Inspect(p2)
	if err != nil {
		t.Fatalf("Inspect UTF-8 failed: %v", err)
	}
	if m2.Package != "org.virdeck.pure" || len(m2.ABIs) != 0 {
		t.Fatalf("unexpected pure APK metadata: %+v", m2)
	}
}

func TestInspectErrors(t *testing.T) {
	if _, err := Inspect("-bad.apk"); err == nil {
		t.Fatal("expected dash-prefixed path to fail")
	}

	missingManifest := buildTestAPK(t, "", false, []string{"arm64-v8a"})
	if _, err := Inspect(missingManifest); err == nil || !strings.Contains(err.Error(), "AndroidManifest.xml") {
		t.Fatalf("expected missing manifest error, got %v", err)
	}

	badAXML := filepath.Join(t.TempDir(), "corrupt.apk")
	f, err := os.Create(badAXML)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("AndroidManifest.xml")
	_, _ = w.Write([]byte{0x03, 0x00, 0x08, 0x00, 0x10, 0x00, 0x00, 0x00, 0x99})
	_ = zw.Close()
	_ = f.Close()
	if _, err := Inspect(badAXML); err == nil {
		t.Fatal("expected corrupt AXML error")
	}
}

func TestCheckARMCompatibility(t *testing.T) {
	if ok, warn := CheckARMCompatibility(nil, nil, "x86", catalog.ARMCompatNone); !ok || warn != "" {
		t.Fatalf("pure-Java APK should always be compatible, got ok=%v warn=%q", ok, warn)
	}

	if ok, _ := CheckARMCompatibility([]string{"arm64-v8a"}, []string{"x86_64", "arm64-v8a"}, "x86_64", catalog.ARMCompatNone); !ok {
		t.Fatal("runtime abilist should take precedence")
	}

	if ok, warn := CheckARMCompatibility([]string{"arm64-v8a"}, []string{"x86_64", "x86"}, "x86_64", catalog.ARMCompatTranslation); ok || warn == "" {
		t.Fatalf("runtime mismatch should warn even if fallback rule assumed translation, got ok=%v warn=%q", ok, warn)
	}

	if ok, _ := CheckARMCompatibility([]string{"arm64-v8a"}, nil, "x86_64", catalog.ARMCompatTranslation); !ok {
		t.Fatal("x86_64 with ARM translation should accept arm64-v8a")
	}

	if ok, warn := CheckARMCompatibility([]string{"arm64-v8a"}, nil, "x86_64", catalog.ARMCompatNone); ok || warn == "" {
		t.Fatalf("x86_64 without ARM translation should warn, got ok=%v warn=%q", ok, warn)
	}

	if ok, _ := CheckARMCompatibility([]string{"armeabi-v7a"}, nil, "arm64-v8a", catalog.ARMCompatNative); !ok {
		t.Fatal("arm64-v8a native image should accept armeabi-v7a")
	}
}

func buildAPKBytes(t *testing.T, pkg string, abis []string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if pkg != "" {
		w, err := zw.Create("AndroidManifest.xml")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(encodeTestAXML([]string{"manifest", "package", pkg}, true, pkg)); err != nil {
			t.Fatal(err)
		}
	}
	for _, abi := range abis {
		w, err := zw.Create("lib/" + abi + "/libdemo.so")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("ELF")); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestInspectAndExtractSplitBundleAPKSAndXAPK(t *testing.T) {
	dir := t.TempDir()
	apksPath := filepath.Join(dir, "demo.apks")
	f, err := os.Create(apksPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, data := range map[string][]byte{
		"splits/config.arm64_v8a.apk": buildAPKBytes(t, "com.example.split", []string{"arm64-v8a"}),
		"splits/config.zh.apk":        buildAPKBytes(t, "com.example.split", nil),
		"splits/base-master.apk":      buildAPKBytes(t, "com.example.split", nil),
	} {
		w, _ := zw.Create(name)
		_, _ = w.Write(data)
	}
	_ = zw.Close()
	_ = f.Close()

	meta, err := Inspect(apksPath)
	if err != nil {
		t.Fatalf("Inspect(.apks) failed: %v", err)
	}
	if !meta.IsSplit || meta.SplitCount != 3 || meta.Package != "com.example.split" || meta.HasOBB {
		t.Fatalf("unexpected .apks metadata: %+v", meta)
	}
	if len(meta.ABIs) != 1 || meta.ABIs[0] != "arm64-v8a" {
		t.Fatalf("unexpected .apks ABIs: %v", meta.ABIs)
	}

	paths, cleanup, err := ExtractSplitAPKs(apksPath)
	if err != nil {
		t.Fatalf("ExtractSplitAPKs failed: %v", err)
	}
	if len(paths) != 3 || !strings.Contains(filepath.Base(paths[0]), "base-master.apk") {
		cleanup()
		t.Fatalf("expected base-master.apk sorted first, got %v", paths)
	}
	tempDir := filepath.Dir(paths[0])
	cleanup()
	if _, statErr := os.Stat(tempDir); !os.IsNotExist(statErr) {
		t.Fatalf("expected cleanup to remove %s", tempDir)
	}

	xapkPath := filepath.Join(dir, "game.xapk")
	xf, err := os.Create(xapkPath)
	if err != nil {
		t.Fatal(err)
	}
	xzw := zip.NewWriter(xf)
	mw, _ := xzw.Create("manifest.json")
	_, _ = mw.Write([]byte(`{"package_name":"com.example.xapk"}`))
	ow, _ := xzw.Create("Android/obb/com.example.xapk/main.1.com.example.xapk.obb")
	_, _ = ow.Write([]byte("OBB"))
	bw, _ := xzw.Create("com.example.xapk.apk")
	_, _ = bw.Write(buildAPKBytes(t, "com.example.xapk", []string{"armeabi-v7a"}))
	_ = xzw.Close()
	_ = xf.Close()

	xMeta, err := Inspect(xapkPath)
	if err != nil {
		t.Fatalf("Inspect(.xapk) failed: %v", err)
	}
	if !xMeta.IsSplit || !xMeta.HasOBB || xMeta.SplitCount != 1 || xMeta.Package != "com.example.xapk" {
		t.Fatalf("unexpected .xapk metadata: %+v", xMeta)
	}
}

func TestSelectSplitAPKsForABIsPicksPreferredNativeOrTranslatedABI(t *testing.T) {
	dir := t.TempDir()
	writeSplit := func(name string, abis []string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, buildAPKBytes(t, "com.example.multi", abis), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	base := writeSplit("base.apk", nil)
	x64 := writeSplit("config.x86_64.apk", []string{"x86_64"})
	arm64 := writeSplit("config.arm64_v8a.apk", []string{"arm64-v8a"})
	arm32 := writeSplit("config.armeabi_v7a.apk", []string{"armeabi-v7a"})
	lang := writeSplit("config.en.apk", nil)

	gotX64 := SelectSplitAPKsForABIs([]string{base, arm32, arm64, x64, lang}, []string{"x86_64", "arm64-v8a"}, "x86_64", catalog.ARMCompatTranslation)
	if len(gotX64) != 3 || gotX64[0] != base || gotX64[1] != x64 || gotX64[2] != lang {
		t.Fatalf("expected x86_64 + non-ABI splits, got %v", gotX64)
	}

	gotARM := SelectSplitAPKsForABIs([]string{base, arm32, arm64, lang}, nil, "x86_64", catalog.ARMCompatTranslation)
	if len(gotARM) != 3 || gotARM[0] != base || gotARM[1] != arm64 || gotARM[2] != lang {
		t.Fatalf("expected arm64-v8a preference under translation fallback, got %v", gotARM)
	}
}

func encodeTestAXMLWithSplit(pkgAttrVal, splitAttrVal string) []byte {
	stringsList := []string{"manifest", "package", pkgAttrVal}
	attrCount := uint16(1)
	if splitAttrVal != "" {
		stringsList = append(stringsList, "split", splitAttrVal)
		attrCount = 2
	}
	var poolBuf bytes.Buffer
	offsets := make([]uint32, len(stringsList))
	for i, s := range stringsList {
		offsets[i] = uint32(poolBuf.Len())
		u16Len := len(utf16.Encode([]rune(s)))
		poolBuf.WriteByte(byte(u16Len))
		poolBuf.WriteByte(byte(len(s)))
		poolBuf.WriteString(s)
		poolBuf.WriteByte(0)
	}
	for poolBuf.Len()%4 != 0 {
		poolBuf.WriteByte(0)
	}
	poolHeaderSize := 28
	stringsStart := poolHeaderSize + len(offsets)*4
	poolChunkSize := stringsStart + poolBuf.Len()
	var poolChunk bytes.Buffer
	_ = binary.Write(&poolChunk, binary.LittleEndian, uint16(resStringPoolType))
	_ = binary.Write(&poolChunk, binary.LittleEndian, uint16(poolHeaderSize))
	_ = binary.Write(&poolChunk, binary.LittleEndian, uint32(poolChunkSize))
	_ = binary.Write(&poolChunk, binary.LittleEndian, uint32(len(stringsList)))
	_ = binary.Write(&poolChunk, binary.LittleEndian, uint32(0))
	_ = binary.Write(&poolChunk, binary.LittleEndian, uint32(stringPoolUTF8Flag))
	_ = binary.Write(&poolChunk, binary.LittleEndian, uint32(stringsStart))
	_ = binary.Write(&poolChunk, binary.LittleEndian, uint32(0))
	for _, off := range offsets {
		_ = binary.Write(&poolChunk, binary.LittleEndian, off)
	}
	poolChunk.Write(poolBuf.Bytes())

	var elemChunk bytes.Buffer
	elemHeaderSize := 16
	attrExtSize := 20
	attrSize := 20
	elemChunkSize := elemHeaderSize + attrExtSize + int(attrCount)*attrSize
	_ = binary.Write(&elemChunk, binary.LittleEndian, uint16(resXMLStartElementType))
	_ = binary.Write(&elemChunk, binary.LittleEndian, uint16(elemHeaderSize))
	_ = binary.Write(&elemChunk, binary.LittleEndian, uint32(elemChunkSize))
	_ = binary.Write(&elemChunk, binary.LittleEndian, uint32(1))
	_ = binary.Write(&elemChunk, binary.LittleEndian, int32(-1))
	_ = binary.Write(&elemChunk, binary.LittleEndian, int32(-1))
	_ = binary.Write(&elemChunk, binary.LittleEndian, int32(0)) // "manifest"
	_ = binary.Write(&elemChunk, binary.LittleEndian, uint16(attrExtSize))
	_ = binary.Write(&elemChunk, binary.LittleEndian, uint16(attrSize))
	_ = binary.Write(&elemChunk, binary.LittleEndian, attrCount)
	_ = binary.Write(&elemChunk, binary.LittleEndian, uint16(0))
	_ = binary.Write(&elemChunk, binary.LittleEndian, uint16(0))
	_ = binary.Write(&elemChunk, binary.LittleEndian, uint16(0))

	writeAttr := func(nameIdx, valIdx int32) {
		_ = binary.Write(&elemChunk, binary.LittleEndian, int32(-1))
		_ = binary.Write(&elemChunk, binary.LittleEndian, nameIdx)
		_ = binary.Write(&elemChunk, binary.LittleEndian, valIdx)
		_ = binary.Write(&elemChunk, binary.LittleEndian, uint16(8))
		elemChunk.WriteByte(0)
		elemChunk.WriteByte(typedValueStringType)
		_ = binary.Write(&elemChunk, binary.LittleEndian, uint32(valIdx))
	}
	writeAttr(1, 2) // package -> pkgAttrVal
	if attrCount == 2 {
		writeAttr(3, 4) // split -> splitAttrVal
	}

	var full bytes.Buffer
	totalSize := 8 + poolChunk.Len() + elemChunk.Len()
	_ = binary.Write(&full, binary.LittleEndian, uint16(resXMLType))
	_ = binary.Write(&full, binary.LittleEndian, uint16(8))
	_ = binary.Write(&full, binary.LittleEndian, uint32(totalSize))
	full.Write(poolChunk.Bytes())
	full.Write(elemChunk.Bytes())
	return full.Bytes()
}

func buildAPKBytesWithSplit(t *testing.T, pkg, splitName string, abis []string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("AndroidManifest.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(encodeTestAXMLWithSplit(pkg, splitName)); err != nil {
		t.Fatal(err)
	}
	for _, abi := range abis {
		lw, err := zw.Create("lib/" + abi + "/libbundle.so")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := lw.Write([]byte("ELF")); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestBundletoolStoreInspectionAndVariantSelection(t *testing.T) {
	dir := t.TempDir()
	apksPath := filepath.Join(dir, "bundletool.apks")
	f, err := os.Create(apksPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	entries := []struct {
		name      string
		splitName string
		abis      []string
	}{
		{"splits/base-master.apk", "", nil},
		{"splits/base-master_2.apk", "", nil},
		{"splits/base-x86_64.apk", "config.x86_64", []string{"x86_64"}},
		{"splits/base-arm64_v8a.apk", "config.arm64_v8a", []string{"arm64-v8a"}},
		{"splits/base-hdpi.apk", "config.hdpi", nil},
		{"splits/base-xhdpi.apk", "config.xhdpi", nil},
		{"splits/base-xxhdpi.apk", "config.xxhdpi", nil},
		{"splits/base-zh.apk", "config.zh", nil},
		{"standalones/standalone-x86_64_xhdpi.apk", "", []string{"x86_64"}},
	}
	for _, ent := range entries {
		hdr := &zip.FileHeader{Name: ent.name, Method: zip.Store}
		w, cerr := zw.CreateHeader(hdr)
		if cerr != nil {
			t.Fatal(cerr)
		}
		if _, werr := w.Write(buildAPKBytesWithSplit(t, "com.example.bundletool", ent.splitName, ent.abis)); werr != nil {
			t.Fatal(werr)
		}
	}
	_ = zw.Close()
	_ = f.Close()

	// Stored inspection should find package, ABIs, and all 9 entries without full extraction.
	meta, err := Inspect(apksPath)
	if err != nil {
		t.Fatalf("Inspect(bundletool.apks) failed: %v", err)
	}
	if meta.Package != "com.example.bundletool" || meta.SplitCount != 9 || len(meta.ABIs) != 2 {
		t.Fatalf("unexpected bundletool Inspect metadata: %+v", meta)
	}

	paths, cleanup, err := ExtractSplitAPKs(apksPath)
	if err != nil {
		t.Fatalf("ExtractSplitAPKs failed: %v", err)
	}
	defer cleanup()

	// Target: x86_64 + 320 DPI -> exactly 1 base (base-master.apk), x86_64, xhdpi, zh (no base-master_2, no standalone).
	sel320 := SelectSplitAPKs(paths, []string{"x86_64", "arm64-v8a"}, "x86_64", catalog.ARMCompatTranslation, 320)
	if len(sel320) != 4 {
		t.Fatalf("expected 4 mutually-exclusive splits for 320 DPI, got %d: %v", len(sel320), sel320)
	}
	joined320 := strings.Join(sel320, "|")
	for _, want := range []string{"base-master.apk", "base-x86_64.apk", "base-xhdpi.apk", "base-zh.apk"} {
		if !strings.Contains(joined320, want) {
			t.Fatalf("expected %q in selected 320 DPI splits, got %v", want, sel320)
		}
	}
	for _, unwanted := range []string{"base-master_2.apk", "base-arm64_v8a.apk", "base-hdpi.apk", "base-xxhdpi.apk", "standalone"} {
		if strings.Contains(joined320, unwanted) {
			t.Fatalf("did not expect conflicting variant %q in %v", unwanted, sel320)
		}
	}

	// Target: 420 DPI -> prefers xxhdpi (480, diff 60) over xhdpi (320, diff 100).
	sel420 := SelectSplitAPKs(paths, []string{"x86_64"}, "x86_64", catalog.ARMCompatNone, 420)
	if !strings.Contains(strings.Join(sel420, "|"), "base-xxhdpi.apk") {
		t.Fatalf("expected base-xxhdpi.apk for 420 DPI, got %v", sel420)
	}

	// Standalone-only bundle picks single best standalone APK.
	saDir := t.TempDir()
	sa1 := filepath.Join(saDir, "standalone-arm64_v8a_hdpi.apk")
	sa2 := filepath.Join(saDir, "standalone-x86_64_xhdpi.apk")
	_ = os.WriteFile(sa1, buildAPKBytesWithSplit(t, "com.example.sa", "", []string{"arm64-v8a"}), 0o600)
	_ = os.WriteFile(sa2, buildAPKBytesWithSplit(t, "com.example.sa", "", []string{"x86_64"}), 0o600)
	saSel := SelectSplitAPKs([]string{sa1, sa2}, []string{"x86_64"}, "x86_64", catalog.ARMCompatNone, 320)
	if len(saSel) != 1 || saSel[0] != sa2 {
		t.Fatalf("expected single matching standalone %s, got %v", sa2, saSel)
	}
}

func TestAXMLAliasedStringPoolDoesNotExhaustMemory(t *testing.T) {
	// Build a compact string pool where 50,000 offsets all point to the same 30 KiB string.
	// Without deduplication by offset, decoding would allocate ~1.5 GiB.
	const entryCount = 50000
	bigStr := strings.Repeat("a", 30000)
	var poolBuf bytes.Buffer
	// Write string 0 ("manifest"), string 1 ("package"), string 2 ("com.example.alias"), string 3 (bigStr)
	var strOffsets [4]uint32
	for idx, s := range []string{"manifest", "package", "com.example.alias", bigStr} {
		strOffsets[idx] = uint32(poolBuf.Len())
		u16Len := len(utf16.Encode([]rune(s)))
		if u16Len >= 0x80 {
			poolBuf.WriteByte(byte(0x80 | (u16Len >> 8)))
			poolBuf.WriteByte(byte(u16Len))
		} else {
			poolBuf.WriteByte(byte(u16Len))
		}
		bLen := len(s)
		if bLen >= 0x80 {
			poolBuf.WriteByte(byte(0x80 | (bLen >> 8)))
			poolBuf.WriteByte(byte(bLen))
		} else {
			poolBuf.WriteByte(byte(bLen))
		}
		poolBuf.WriteString(s)
		poolBuf.WriteByte(0)
	}
	for poolBuf.Len()%4 != 0 {
		poolBuf.WriteByte(0)
	}
	poolHeaderSize := 28
	stringsStart := poolHeaderSize + entryCount*4
	poolChunkSize := stringsStart + poolBuf.Len()
	var poolChunk bytes.Buffer
	_ = binary.Write(&poolChunk, binary.LittleEndian, uint16(resStringPoolType))
	_ = binary.Write(&poolChunk, binary.LittleEndian, uint16(poolHeaderSize))
	_ = binary.Write(&poolChunk, binary.LittleEndian, uint32(poolChunkSize))
	_ = binary.Write(&poolChunk, binary.LittleEndian, uint32(entryCount))
	_ = binary.Write(&poolChunk, binary.LittleEndian, uint32(0))
	_ = binary.Write(&poolChunk, binary.LittleEndian, uint32(stringPoolUTF8Flag))
	_ = binary.Write(&poolChunk, binary.LittleEndian, uint32(stringsStart))
	_ = binary.Write(&poolChunk, binary.LittleEndian, uint32(0))
	for i := 0; i < entryCount; i++ {
		if i < 3 {
			_ = binary.Write(&poolChunk, binary.LittleEndian, strOffsets[i])
		} else {
			_ = binary.Write(&poolChunk, binary.LittleEndian, strOffsets[3])
		}
	}
	poolChunk.Write(poolBuf.Bytes())

	pool, err := parseStringPool(poolChunk.Bytes())
	if err != nil {
		t.Fatalf("parseStringPool failed: %v", err)
	}
	if len(pool) != entryCount || pool[3] != bigStr || pool[2] != "com.example.alias" {
		t.Fatalf("unexpected aliased string pool output")
	}
}
