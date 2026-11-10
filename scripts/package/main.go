// Package main builds unsigned development binaries for each supported host.
package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"virdeck/assets"
)

type packageFile struct {
	disk, name string
	mode       os.FileMode
}

func xmlText(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func writeArchive(dest, folder string, files []packageFile) (err error) {
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer func() {
		if e := f.Close(); err == nil {
			err = e
		}
	}()
	z := zip.NewWriter(f)
	defer func() {
		if e := z.Close(); err == nil {
			err = e
		}
	}()
	for _, file := range files {
		in, e := os.Open(file.disk)
		if e != nil {
			return e
		}
		header := &zip.FileHeader{Name: folder + "/" + filepath.ToSlash(file.name), Method: zip.Deflate}
		header.SetMode(file.mode)
		out, e := z.CreateHeader(header)
		if e == nil {
			_, e = io.Copy(out, in)
		}
		_ = in.Close()
		if e != nil {
			return e
		}
	}
	return nil
}

func copyFile(src, dst string) {
	if src == dst {
		return
	}
	in, err := os.Open(src)
	if err != nil {
		return
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return
	}
	defer out.Close()
	_, _ = io.Copy(out, in)
}

func build(goos, arch, output, pkg string, gui bool) error {
	env := []string{}
	for _, v := range os.Environ() {
		key, _, _ := strings.Cut(v, "=")
		if !strings.EqualFold(key, "GOOS") && !strings.EqualFold(key, "GOARCH") && !strings.EqualFold(key, "CGO_ENABLED") {
			env = append(env, v)
		}
	}
	env = append(env, "GOOS="+goos, "GOARCH="+arch, "CGO_ENABLED=0")
	args := []string{"build", "-trimpath"}
	ldflags := "-s -w"
	if gui {
		args = append(args, "-tags", "mygo_noinspector")
		ldflags += " -X github.com/egoist/mygo.production=1"
		if goos == "windows" {
			ldflags = "-H=windowsgui " + ldflags
		}
	}
	args = append(args, "-ldflags", ldflags, "-o", output, pkg)
	cmd := exec.Command("go", args...)
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func artifactNames(productName, version, goos, arch string) (guiName, cliName string) {
	verTag := "v" + strings.TrimPrefix(version, "v")
	exeExt := ""
	if goos == "windows" {
		exeExt = ".exe"
	}
	guiName = fmt.Sprintf("%s-%s-%s-%s%s", productName, verTag, goos, arch, exeExt)
	cliName = fmt.Sprintf("virdeck-cli-%s-%s-%s%s", verTag, goos, arch, exeExt)
	return guiName, cliName
}

func run() error {
	goos := flag.String("os", runtime.GOOS, "windows, darwin, linux")
	arch := flag.String("arch", runtime.GOARCH, "amd64 or arm64 (macOS)")
	output := flag.String("out", "dist", "output directory")
	flag.Parse()
	valid := (*goos == "windows" && *arch == "amd64") || (*goos == "darwin" && (*arch == "amd64" || *arch == "arm64")) || (*goos == "linux" && *arch == "amd64")
	if !valid {
		return fmt.Errorf("unsupported distribution target %s/%s", *goos, *arch)
	}
	if _, err := os.Stat("go.mod"); err != nil {
		return fmt.Errorf("run from the project root: %w", err)
	}

	guiName, cliName := artifactNames(assets.Product.Name, assets.Product.Version, *goos, *arch)

	if err := os.MkdirAll(*output, 0755); err != nil {
		return err
	}

	guiPath := filepath.Join(*output, guiName)
	if err := build(*goos, *arch, guiPath, "./cmd/app", true); err != nil {
		return err
	}

	cliPath := filepath.Join(*output, cliName)
	if err := build(*goos, *arch, cliPath, "./cmd/cli", false); err != nil {
		return err
	}

	// 针对 macOS 生成同名 .app 结构
	if *goos == "darwin" {
		verTag := "v" + strings.TrimPrefix(assets.Product.Version, "v")
		appRoot := filepath.Join(*output, fmt.Sprintf("%s-%s-%s-%s.app", assets.Product.Name, verTag, *goos, *arch))
		macOSDir := filepath.Join(appRoot, "Contents", "MacOS")
		if err := os.MkdirAll(macOSDir, 0755); err == nil {
			copyFile(guiPath, filepath.Join(macOSDir, "VirDeck"))
			plist := `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict>` +
				`<key>CFBundleName</key><string>` + xmlText(assets.Product.Name) + `</string><key>CFBundleDisplayName</key><string>` + xmlText(assets.Product.Name) + `</string>` +
				`<key>CFBundleExecutable</key><string>VirDeck</string><key>CFBundleIdentifier</key><string>io.virdeck.app</string>` +
				`<key>CFBundlePackageType</key><string>APPL</string><key>CFBundleShortVersionString</key><string>` + xmlText(assets.Product.Version) + `</string>` +
				`<key>CFBundleVersion</key><string>` + xmlText(assets.Product.Version) + `</string><key>NSHighResolutionCapable</key><true/>` +
				`<key>NSAppleEventsUsageDescription</key><string>Manage projection windows.</string></dict></plist>`
			_ = os.WriteFile(filepath.Join(appRoot, "Contents", "Info.plist"), []byte(plist), 0644)
		}
	}

	// 同时同步更新未带版本号的标准文件名，方便本地直接运行与调用
	exeExt := ""
	if *goos == "windows" {
		exeExt = ".exe"
	}
	copyFile(guiPath, filepath.Join(*output, assets.Product.Name+exeExt))
	copyFile(cliPath, filepath.Join(*output, "virdeck-cli"+exeExt))

	// 注释掉打包成 zip 的部分，直接产出 VirDeck 和 virdeck-cli 可执行文件
	/*
		name := fmt.Sprintf("VirDeck-%s-%s", *goos, *arch)
		files := []packageFile{{guiPath, guiName, 0755}, {cliPath, cliName, 0755}}
		archive := filepath.Join(*output, name+".zip")
		if err := writeArchive(archive, name, files); err != nil {
			return err
		}
		fmt.Println(archive)
	*/

	fmt.Printf("Built GUI binary: %s\n", guiPath)
	fmt.Printf("Built CLI binary: %s\n", cliPath)
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
