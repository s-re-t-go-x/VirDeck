package adb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func init() {
	if os.Getenv("VIRDECK_TEST_ADB_HELPER") != "1" {
		return
	}
	marker := os.Getenv("VIRDECK_TEST_ADB_MARKER")
	args := strings.Join(os.Args[1:], " ")
	switch {
	case strings.HasSuffix(args, "devices"):
		if extra := os.Getenv("VIRDECK_TEST_ADB_DEVICES_EXTRA"); extra != "" {
			fmt.Println("List of devices attached")
			fmt.Print(extra)
			if !strings.HasSuffix(extra, "\n") {
				fmt.Println()
			}
			os.Exit(0)
		}
		fmt.Println("List of devices attached")
		fmt.Println("emulator-5554 offline")
		os.Exit(0)
	case strings.HasSuffix(args, "-s emulator-5554 emu avd name"):
		fmt.Println("avd_alive")
		fmt.Println("OK")
		os.Exit(0)
	case strings.HasSuffix(args, "-s emulator-5556 emu avd name"):
		os.Exit(1)
	case strings.HasSuffix(args, "shell getprop sys.boot_completed"):
		b, _ := os.ReadFile(marker)
		if string(b) == "ready" {
			fmt.Println("1")
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, "adb: device unauthorized")
		os.Exit(1)
	case strings.HasSuffix(args, "shell getprop ro.product.cpu.abilist"):
		fmt.Println("x86_64,arm64-v8a,armeabi-v7a")
		os.Exit(0)
	case strings.HasSuffix(args, "shell pm path android"):
		fmt.Println("package:/system/framework/framework-res.apk")
		os.Exit(0)
	case strings.Contains(args, "install -r -g "):
		fmt.Println("Success")
		os.Exit(0)
	case strings.Contains(args, "install-multiple -r -g base.apk split.apk"):
		fmt.Println("Success")
		os.Exit(0)
	case strings.Contains(args, "cmd package resolve-activity --brief -c android.intent.category.LAUNCHER com.example.demo"):
		fmt.Println("priority=0 preferredOrder=0 match=0x108000 specificIndex=-1 isDefault=true")
		fmt.Println("com.example.demo/.MainActivity")
		os.Exit(0)
	case strings.Contains(args, "cmd package resolve-activity --brief -c android.intent.category.LAUNCHER com.example.serviceonly"):
		fmt.Println("No activity found")
		os.Exit(0)
	case strings.HasSuffix(args, "shell am start -n com.example.demo/.MainActivity"):
		fmt.Println("Starting: Intent { cmp=com.example.demo/.MainActivity }")
		os.Exit(0)
	case strings.Contains(args, "emu geo fix 116.407400 39.904200"):
		fmt.Println("OK")
		os.Exit(0)
	case strings.HasSuffix(args, "exec-out screencap -p"):
		_, _ = os.Stdout.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0})
		os.Exit(0)
	case strings.HasSuffix(args, "shell pm list packages -3"):
		fmt.Println("package:com.zeta.app")
		fmt.Println("package:com.alpha.app")
		fmt.Println("package:com.zeta.app")
		os.Exit(0)
	case strings.HasSuffix(args, "shell pm list packages"):
		fmt.Println("package:com.android.settings")
		fmt.Println("package:com.alpha.app")
		os.Exit(0)
	case strings.HasSuffix(args, "shell am force-stop com.alpha.app"):
		os.Exit(0)
	case strings.HasSuffix(args, "shell pm clear com.alpha.app"):
		fmt.Println("Success")
		os.Exit(0)
	case strings.HasSuffix(args, "shell pm clear com.fail.app"):
		fmt.Println("Failed")
		os.Exit(0)
	case strings.HasSuffix(args, "uninstall com.alpha.app"):
		fmt.Println("Success")
		os.Exit(0)
	case strings.HasSuffix(args, "uninstall com.fail.app"):
		fmt.Println("Failure [DELETE_FAILED_INTERNAL_ERROR]")
		os.Exit(0)
	case strings.HasSuffix(args, "shell sm fstrim"):
		fmt.Println("Trimmed")
		os.Exit(0)
	case strings.HasSuffix(args, "kill-server"):
		if err := os.WriteFile(marker, []byte("killed"), 0600); err != nil {
			os.Exit(2)
		}
	case strings.HasSuffix(args, "start-server"):
		b, _ := os.ReadFile(marker)
		if string(b) != "killed" {
			os.Exit(2)
		}
		if err := os.WriteFile(marker, []byte("ready"), 0600); err != nil {
			os.Exit(2)
		}
	default:
		os.Exit(2)
	}
	os.Exit(0)
}

func testADB(t *testing.T, isolated bool) (*Client, string) {
	t.Helper()
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "server-state")
	t.Setenv("VIRDECK_TEST_ADB_HELPER", "1")
	t.Setenv("VIRDECK_TEST_ADB_MARKER", marker)
	c := NewClient(exe)
	env := os.Environ()
	if isolated {
		env = append(env, "ANDROID_ADB_SERVER_PORT=5137", "ADB_VENDOR_KEYS="+filepath.Join(dir, "adbkey"))
	}
	c.SetEnv(env)
	return c, marker
}

func TestWaitForBootRecoversUnauthorizedDedicatedServer(t *testing.T) {
	c, _ := testADB(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.WaitForBoot(ctx, 5554, 10*time.Millisecond); err != nil {
		t.Fatalf("boot should recover from stale server keys: %v", err)
	}
}

func TestWaitForBootPreservesUnauthorizedWithoutRestartingDefaultServer(t *testing.T) {
	c, marker := testADB(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err := c.WaitForBoot(ctx, 5554, 10*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("want diagnostic containing last ADB error, got %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("must not restart the default/shared ADB server")
	}
}

func TestHasEmulatorsCountsOfflineAndReportsFailure(t *testing.T) {
	c, _ := testADB(t, true)
	active, err := c.HasEmulators(context.Background())
	if err != nil || !active {
		t.Fatalf("offline emulator must block cleanup: %v, %v", active, err)
	}
	c.SetPath(filepath.Join(t.TempDir(), "missing-adb.exe"))
	if _, err := c.HasEmulators(context.Background()); err == nil {
		t.Fatal("detection failure must be reported")
	}
}

func TestIsInstallCommandAndGetABIList(t *testing.T) {
	for _, args := range [][]string{
		{"-s", "emulator-5554", "install", "-r", "app.apk"},
		{"-s", "emulator-5554", "install-multiple", "-r", "base.apk", "split.apk"},
		{"install", "app.apk"},
	} {
		if !isInstallCommand(args) {
			t.Fatalf("expected isInstallCommand(%v) = true", args)
		}
	}
	if isInstallCommand([]string{"-s", "emulator-5554", "shell", "pm", "install"}) {
		t.Fatal("shell pm install must not match top-level install command")
	}

	c, _ := testADB(t, true)
	abis, err := c.GetABIList(context.Background(), 5554)
	if err != nil {
		t.Fatalf("GetABIList: %v", err)
	}
	if len(abis) != 3 || abis[0] != "x86_64" || abis[1] != "arm64-v8a" || abis[2] != "armeabi-v7a" {
		t.Fatalf("unexpected ABI list: %v", abis)
	}
}

func TestInstallOptionsErrorTranslationAndLaunchHelpers(t *testing.T) {
	c, _ := testADB(t, true)
	ctx := context.Background()

	if err := c.CheckPackageManagerReady(ctx, 5554); err != nil {
		t.Fatalf("CheckPackageManagerReady failed: %v", err)
	}

	if _, err := c.InstallAPKWithOptions(ctx, 5554, "-evil.apk", InstallOptions{GrantPermissions: true}); err == nil {
		t.Fatal("dash-prefixed apk path must be rejected")
	}
	out, err := c.InstallAPKWithOptions(ctx, 5554, "app.apk", InstallOptions{GrantPermissions: true})
	if err != nil || !strings.Contains(out, "Success") {
		t.Fatalf("InstallAPKWithOptions(-g) failed: out=%q err=%v", out, err)
	}
	if _, err := c.InstallMultipleAPKsWithOptions(ctx, 5554, nil, InstallOptions{}); err == nil {
		t.Fatal("empty split apk list must be rejected")
	}
	if _, err := c.InstallMultipleAPKsWithOptions(ctx, 5554, []string{"base.apk", "-bad.apk"}, InstallOptions{}); err == nil {
		t.Fatal("dash-prefixed split apk path must be rejected")
	}
	multiOut, err := c.InstallMultipleAPKsWithOptions(ctx, 5554, []string{"base.apk", "split.apk"}, InstallOptions{GrantPermissions: true})
	if err != nil || !strings.Contains(multiOut, "Success") {
		t.Fatalf("InstallMultipleAPKsWithOptions(-g) failed: out=%q err=%v", multiOut, err)
	}

	comp, err := c.ResolveLauncherActivity(ctx, 5554, "com.example.demo")
	if err != nil || comp != "com.example.demo/.MainActivity" {
		t.Fatalf("ResolveLauncherActivity mismatch: comp=%q err=%v", comp, err)
	}
	noneComp, err := c.ResolveLauncherActivity(ctx, 5554, "com.example.serviceonly")
	if err != nil || noneComp != "" {
		t.Fatalf("expected empty component for service-only pkg, got %q err=%v", noneComp, err)
	}
	if err := c.LaunchActivity(ctx, 5554, "bad component"); err == nil {
		t.Fatal("component with whitespace must be rejected")
	}
	if err := c.LaunchActivity(ctx, 5554, comp); err != nil {
		t.Fatalf("LaunchActivity failed: %v", err)
	}

	cases := []struct {
		raw  string
		want string
	}{
		{"Failure [INSTALL_FAILED_VERSION_DOWNGRADE]", "降级覆盖"},
		{"Failure [INSTALL_FAILED_UPDATE_INCOMPATIBLE]", "卸载会清除该应用本地数据"},
		{"Failure [INSTALL_FAILED_INSUFFICIENT_STORAGE]", "存储空间不足"},
		{"Failure [INSTALL_FAILED_NO_MATCHING_ABIS]", "ARM 转译"},
		{"other failure", "other failure"},
	}
	for _, tc := range cases {
		got := TranslateInstallError(errors.New(tc.raw))
		if !strings.Contains(got, tc.want) {
			t.Fatalf("TranslateInstallError(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestSetLocationValidationAndArgOrder(t *testing.T) {
	c, _ := testADB(t, false)
	if err := c.SetLocation(context.Background(), 5554, 116.4074, 39.9042); err != nil {
		t.Fatalf("SetLocation failed: %v", err)
	}
	if err := c.SetLocation(context.Background(), 5554, 200, 39.9); err == nil {
		t.Fatal("expected out-of-range longitude error")
	}
	if err := c.SetLocation(context.Background(), 5554, 116.4, -95); err == nil {
		t.Fatal("expected out-of-range latitude error")
	}
}

func TestScreenshotAndPackageCommands(t *testing.T) {
	c, _ := testADB(t, false)
	raw, err := c.Screenshot(context.Background(), 5554)
	if err != nil {
		t.Fatalf("Screenshot failed: %v", err)
	}
	if len(raw) < 8 || string(raw[:4]) != "\x89PNG" {
		t.Fatalf("unexpected screenshot header: %x", raw)
	}

	pkgs3, err := c.ListPackages(context.Background(), 5554, true)
	if err != nil {
		t.Fatalf("ListPackages(-3) failed: %v", err)
	}
	if len(pkgs3) != 2 || pkgs3[0] != "com.alpha.app" || pkgs3[1] != "com.zeta.app" {
		t.Fatalf("unexpected sorted deduplicated packages: %v", pkgs3)
	}

	pkgsAll, err := c.ListPackages(context.Background(), 5554, false)
	if err != nil {
		t.Fatalf("ListPackages(all) failed: %v", err)
	}
	if len(pkgsAll) != 2 || pkgsAll[0] != "com.alpha.app" || pkgsAll[1] != "com.android.settings" {
		t.Fatalf("unexpected all packages: %v", pkgsAll)
	}

	if err := c.ForceStopPackage(context.Background(), 5554, "com.alpha.app"); err != nil {
		t.Fatalf("ForceStopPackage failed: %v", err)
	}
	if err := c.ForceStopPackage(context.Background(), 5554, "-evil"); err == nil {
		t.Fatal("expected token validation error for -evil")
	}
	if err := c.ClearPackageData(context.Background(), 5554, "com.alpha.app"); err != nil {
		t.Fatalf("ClearPackageData failed: %v", err)
	}
	if err := c.ClearPackageData(context.Background(), 5554, "com.fail.app"); err == nil {
		t.Fatal("expected error on pm clear Failed")
	}
	if err := c.UninstallPackage(context.Background(), 5554, "com.alpha.app"); err != nil {
		t.Fatalf("UninstallPackage failed: %v", err)
	}
	if err := c.UninstallPackage(context.Background(), 5554, "com.fail.app"); err == nil {
		t.Fatal("expected error on uninstall Failure")
	}
	out, err := c.TrimStorage(context.Background(), 5554)
	if err != nil || !strings.Contains(out, "Trimmed") {
		t.Fatalf("TrimStorage unexpected result: out=%q err=%v", out, err)
	}
}

func TestDiscoverEmulatorsStrictCountsUnknownAndOffline(t *testing.T) {
	c, _ := testADB(t, false)
	// Default helper prints "emulator-5554 offline".
	active, unknown, err := c.DiscoverEmulatorsStrict(context.Background())
	if err != nil {
		t.Fatalf("DiscoverEmulatorsStrict failed: %v", err)
	}
	if len(active) != 0 || unknown != 1 {
		t.Fatalf("expected offline device counted as unknown=1, got active=%v unknown=%d", active, unknown)
	}

	t.Setenv("VIRDECK_TEST_ADB_DEVICES_EXTRA", "emulator-5554 device\nemulator-5556 device\n")
	c2, _ := testADB(t, false)
	active, unknown, err = c2.DiscoverEmulatorsStrict(context.Background())
	if err != nil {
		t.Fatalf("DiscoverEmulatorsStrict multi failed: %v", err)
	}
	if active["avd_alive"] != 5554 || unknown != 1 {
		t.Fatalf("expected active={avd_alive:5554} unknown=1, got active=%v unknown=%d", active, unknown)
	}
}

func TestADBRejectsShellMetacharactersInPackageAndComponent(t *testing.T) {
	c, _ := testADB(t, false)
	ctx := context.Background()
	for _, badPkg := range []string{"com.demo;id", "com.demo$(id)", "com.demo|cat", "com.demo`id`", "badpkg"} {
		if err := c.ForceStopPackage(ctx, 5554, badPkg); err == nil {
			t.Fatalf("expected ForceStopPackage to reject %q", badPkg)
		}
		if _, err := c.ResolveLauncherActivity(ctx, 5554, badPkg); err == nil {
			t.Fatalf("expected ResolveLauncherActivity to reject %q", badPkg)
		}
	}
	for _, badComp := range []string{"com.demo/.Main;id", "com.demo/.Main$(id)", "com.demo/"} {
		if err := c.LaunchActivity(ctx, 5554, badComp); err == nil {
			t.Fatalf("expected LaunchActivity to reject %q", badComp)
		}
	}
	if got := parseResolvedComponent("com.demo/.Main;id\n", "com.demo"); got != "" {
		t.Fatalf("parseResolvedComponent accepted unsafe component: %q", got)
	}
}
