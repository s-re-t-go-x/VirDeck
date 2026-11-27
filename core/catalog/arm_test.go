package catalog

import "testing"

func TestImageARMCompatRules(t *testing.T) {
	cases := []struct {
		api, tag, abi string
		want          ARMCompat
	}{
		{"30", "google_apis_playstore", "arm64-v8a", ARMCompatNative},
		{"34", "default", "armeabi-v7a", ARMCompatNative},
		{"30", "google_apis_playstore", "x86_64", ARMCompatTranslation},
		{"34", "google_apis", "x86_64", ARMCompatTranslation},
		{"34", "default", "x86_64", ARMCompatNone},
		{"28", "google_apis_playstore", "x86_64", ARMCompatNone},
		{"25", "google_apis_playstore", "x86", ARMCompatNone},
	}
	for _, tc := range cases {
		if got := ImageARMCompat(tc.api, tc.tag, tc.abi); got != tc.want {
			t.Errorf("ImageARMCompat(%q, %q, %q) = %q, want %q", tc.api, tc.tag, tc.abi, got, tc.want)
		}
	}
}
