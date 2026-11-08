package assets

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Product metadata is the single source for the interface and Windows resources.
//
//go:embed product.json
var productJSON []byte

type ProductInfo struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	CompanyName string `json:"companyName"`
	Copyright   string `json:"copyright"`
}

var Product = func() ProductInfo {
	var info ProductInfo
	if err := json.Unmarshal(productJSON, &info); err != nil {
		panic(fmt.Errorf("product metadata: %w", err))
	}
	if info.Name == "" || info.Description == "" {
		panic("product metadata must include name and description")
	}
	parts := strings.Split(info.Version, ".")
	if len(parts) != 3 {
		panic("product version must contain three numeric components")
	}
	for _, part := range parts {
		if _, err := strconv.ParseUint(part, 10, 16); err != nil {
			panic("invalid product version: " + info.Version)
		}
	}
	return info
}()
