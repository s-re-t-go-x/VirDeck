package catalog

import (
	"strconv"
	"strings"
)

// ARMCompat indicates how a system image supports ARM native libraries (arm64-v8a / armeabi-v7a).
type ARMCompat string

const (
	ARMCompatNative      ARMCompat = "native"
	ARMCompatTranslation ARMCompat = "translation"
	ARMCompatNone        ARMCompat = "none"
)

// ImageARMCompat classifies whether a system image provides native ARM execution,
// built-in ChromeOS libndk_translation on x86_64, or x86-only execution.
func ImageARMCompat(apiLevel, tagID, abi string) ARMCompat {
	abiLower := strings.ToLower(strings.TrimSpace(abi))
	if strings.HasPrefix(abiLower, "arm") {
		return ARMCompatNative
	}
	if abiLower != "x86_64" {
		return ARMCompatNone
	}
	tagLower := strings.ToLower(strings.TrimSpace(tagID))
	if tagLower != "google_apis" && tagLower != "google_apis_playstore" {
		return ARMCompatNone
	}
	apiStr := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(apiLevel)), "android-")
	apiNum, err := strconv.ParseFloat(apiStr, 64)
	if err != nil || apiNum < 30 {
		return ARMCompatNone
	}
	return ARMCompatTranslation
}
