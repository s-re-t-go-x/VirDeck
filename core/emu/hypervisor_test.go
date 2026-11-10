package emu

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMacHypervisorRequiresPositiveCapability(t *testing.T) {
	for _, tc := range []struct {
		output string
		err    error
		want   bool
	}{{"1\n", nil, true}, {"0\n", nil, false}, {"", errors.New("sysctl unavailable"), false}, {"unknown", nil, false}} {
		ok, message := macHypervisorStatus([]byte(tc.output), tc.err)
		if ok != tc.want || message == "" {
			t.Errorf("capability %q: %v, %s", tc.output, ok, message)
		}
	}
}

func TestLinuxKVMReportsAccessAndMissingDevice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kvm")
	if ok, msg := checkKVMAccess(path); ok || !strings.Contains(msg, "KVM") {
		t.Fatalf("missing KVM = %v, %s", ok, msg)
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if ok, msg := checkKVMAccess(path); !ok || msg == "" {
		t.Fatalf("accessible KVM = %v, %s", ok, msg)
	}
	// A directory cannot be opened read/write and exercises a real access failure.
	if ok, msg := checkKVMAccess(t.TempDir()); ok || !strings.Contains(msg, "权限") {
		t.Fatalf("inaccessible KVM = %v, %s", ok, msg)
	}
}
