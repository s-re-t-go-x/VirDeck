package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"virdeck/adapters/cli"
	"virdeck/core/service"
)

func main() {
	registryPath := strings.TrimSpace(os.Getenv("VIRDECK_REGISTRY"))
	mgr, err := service.NewManager(service.Config{
		DataDir:      strings.TrimSpace(os.Getenv("VIRDECK_DATA_DIR")),
		RegistryPath: registryPath,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化核心服务失败: %v\n", err)
		os.Exit(1)
	}

	if err := cli.Run(context.Background(), mgr, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		os.Exit(1)
	}
}
