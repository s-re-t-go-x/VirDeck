package mygonative

import (
	"strings"
	"testing"
	"virdeck/core/service"
)

func TestCrossPlatformEnvironmentGuidance(t *testing.T) {
	for _, tc := range []struct{ host, accelerator, fix string }{{"windows", "WHPX", "Windows"}, {"darwin", "Hypervisor", "macOS"}, {"linux", "KVM", "/dev/kvm"}} {
		t.Run(tc.host, func(t *testing.T) {
			checks := buildEnvChecksForHost(service.EnvStatus{DataDir: "/tmp/设备", PathWritableOK: true}, tc.host)
			found := false
			for _, check := range checks {
				if strings.Contains(check.name, tc.accelerator) {
					found = true
					if !strings.Contains(check.fix, tc.fix) {
						t.Fatalf("wrong accelerator remedy: %s", check.fix)
					}
				}
				if tc.host != "windows" && check.name == "数据目录与权限" && !check.ok {
					t.Fatal("Unix writable Unicode path incorrectly blocked")
				}
				if tc.host != "windows" && strings.Contains(check.fix, "scrcpy-win64") {
					t.Fatal("Windows download instructions leaked to Unix")
				}
			}
			if !found {
				t.Fatalf("missing %s check", tc.accelerator)
			}
		})
	}
}
