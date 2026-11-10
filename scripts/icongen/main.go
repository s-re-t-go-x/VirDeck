// Command icongen renders the source SVG with MyGo and writes PNG/ICO assets.
// Run from the repository root or with go generate ./assets.
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/egoist/mygo/ui"
	"virdeck/assets"
)

func main() {
	root, err := os.Getwd()
	must(err)
	if filepath.Base(root) == "assets" {
		root = filepath.Dir(root)
	}

	// 1. Generate GUI icon assets (icon.png, icon-preview.png, icon.ico)
	generateIconAssets(root, "icon.svg", "icon")

	// 2. Generate CLI icon assets (icon-cli.png, icon-cli-preview.png, icon-cli.ico)
	generateIconAssets(root, "icon-cli.svg", "icon-cli")

	// 3. Generate Windows embedded resources (.syso) for app and cli
	generateWindowsResources(root)

	fmt.Printf("Updated %s %s GUI & CLI icons and Windows version resources.\n", assets.Product.Name, assets.Product.Version)
}

func generateIconAssets(root, svgName, baseName string) {
	dir := filepath.Join(root, "assets")
	src, err := os.ReadFile(filepath.Join(dir, svgName))
	must(err)
	svg, err := ui.ParseSVG(src)
	must(err)

	render := func(size int) []byte {
		theme := ui.LightTheme()
		theme.Background = ui.Transparent
		tester := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(theme)
			ui.Image(c, svg).Size(float32(size), float32(size))
		}, size, size)
		tester.Frame()
		var b bytes.Buffer
		must(png.Encode(&b, tester.Image()))
		return b.Bytes()
	}

	must(os.WriteFile(filepath.Join(dir, baseName+".png"), render(256), 0644))
	must(os.WriteFile(filepath.Join(dir, baseName+"-preview.png"), render(512), 0644))

	sizes := []int{16, 20, 24, 32, 40, 48, 64, 128, 256}
	images := make([][]byte, len(sizes))
	for i, size := range sizes {
		images[i] = render(size)
	}

	var ico bytes.Buffer
	must(binary.Write(&ico, binary.LittleEndian, [3]uint16{0, 1, uint16(len(sizes))}))
	offset := 6 + 16*len(sizes)
	for i, size := range sizes {
		dim := byte(size)
		ico.Write([]byte{dim, dim, 0, 0})
		must(binary.Write(&ico, binary.LittleEndian, [2]uint16{1, 32}))
		must(binary.Write(&ico, binary.LittleEndian, uint32(len(images[i]))))
		must(binary.Write(&ico, binary.LittleEndian, uint32(offset)))
		offset += len(images[i])
	}
	for _, img := range images {
		ico.Write(img)
	}
	must(os.WriteFile(filepath.Join(dir, baseName+".ico"), ico.Bytes(), 0644))
}

func generateWindowsResources(root string) {
	dir, err := os.MkdirTemp("", "virdeck-resources-")
	must(err)
	defer os.RemoveAll(dir)

	targets := []struct {
		pkg         string
		filename    string
		description string
		iconFile    string
	}{
		{"app", assets.Product.Name + ".exe", assets.Product.Description, "icon.ico"},
		{"cli", "virdeck-cli.exe", assets.Product.Name + " 命令行工具", "icon-cli.ico"},
	}

	for _, target := range targets {
		iconData, err := os.ReadFile(filepath.Join(root, "assets", target.iconFile))
		must(err)
		must(os.WriteFile(filepath.Join(dir, target.iconFile), iconData, 0600))

		version := assets.Product.Version + ".0"
		info := map[string]string{
			"CompanyName":      assets.Product.CompanyName,
			"FileDescription":  target.description,
			"FileVersion":      version,
			"InternalName":     target.filename[:len(target.filename)-4],
			"LegalCopyright":   assets.Product.Copyright,
			"OriginalFilename": target.filename,
			"ProductName":      assets.Product.Name,
			"ProductVersion":   assets.Product.Version,
		}
		resources := map[string]any{
			"RT_GROUP_ICON": map[string]any{"#1": map[string]any{"0000": target.iconFile}},
			"RT_VERSION": map[string]any{"#1": map[string]any{"0000": map[string]any{
				"fixed": map[string]string{"file_version": version, "product_version": version},
				"info":  map[string]any{"0409": info, "0804": info},
			}}},
		}
		data, err := json.MarshalIndent(resources, "", "  ")
		must(err)
		config := filepath.Join(dir, target.pkg+".json")
		must(os.WriteFile(config, data, 0600))

		var cmd *exec.Cmd
		localWinres := filepath.Join(root, "scripts", "icongen", "go-winres.exe")
		if _, err := os.Stat(localWinres); err == nil {
			cmd = exec.Command(localWinres, "make", "--in", config, "--arch", "amd64", "--out", filepath.Join(root, "cmd", target.pkg, "icon"))
		} else if path, err := exec.LookPath("go-winres"); err == nil {
			cmd = exec.Command(path, "make", "--in", config, "--arch", "amd64", "--out", filepath.Join(root, "cmd", target.pkg, "icon"))
		} else {
			cmd = exec.Command("go", "run", "github.com/tc-hib/go-winres@v0.3.3", "make", "--in", config, "--arch", "amd64", "--out", filepath.Join(root, "cmd", target.pkg, "icon"))
		}
		cmd.Dir = root
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		must(cmd.Run())
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
