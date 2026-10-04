package monitor

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCoreDoesNotImportAdapters(t *testing.T) {
	for _, folder := range []string{".", "../domain"} {
		entries, err := os.ReadDir(folder)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(folder, entry.Name())
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, dependency := range file.Imports {
				name, err := strconv.Unquote(dependency.Path.Value)
				if err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(name, "github.com/golosoman/codex-reset-watcher/internal/") && name != "github.com/golosoman/codex-reset-watcher/internal/domain" {
					t.Errorf("%s imports adapter %s", path, name)
				}
			}
		}
	}
}
