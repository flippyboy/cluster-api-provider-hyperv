package hyperv

import (
	"embed"
	"fmt"
	"strings"
)

//go:embed scripts/*.ps1
var scriptFS embed.FS

func loadScript(name string) (string, error) {
	body, err := scriptFS.ReadFile("scripts/" + name + ".ps1")
	if err != nil {
		return "", fmt.Errorf("load script %s: %w", name, err)
	}
	marker := "# script: " + name
	if !strings.HasPrefix(string(body), marker) {
		return "", fmt.Errorf("script %s does not start with %q", name, marker)
	}
	return string(body), nil
}
