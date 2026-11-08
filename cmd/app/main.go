package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"virdeck/adapters/cli"
	"virdeck/adapters/mygonative"
	"virdeck/core/service"
)

func main() {
	registryPath := strings.TrimSpace(os.Getenv("VIRDECK_REGISTRY"))
	mgr, err := service.NewManager(service.Config{
		DataDir:      strings.TrimSpace(os.Getenv("VIRDECK_DATA_DIR")),
		RegistryPath: registryPath,
	})
	if err != nil {
		showStartupError("初始化核心服务失败：" + err.Error())
		os.Exit(1)
	}

	args := os.Args[1:]
	if len(args) > 0 {
		if strings.EqualFold(args[0], "cli") {
			args = args[1:]
		}
		if err := cli.Run(context.Background(), mgr, args, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "错误: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := mygonative.Run(mgr); err != nil {
		showStartupError("界面运行异常：" + err.Error())
		os.Exit(1)
	}
}
