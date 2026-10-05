package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

func TestTranslationSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "watcher.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Translation(ctx, "missing"); err != nil || ok {
		t.Fatalf("unexpected cache hit: %v %v", ok, err)
	}
	if err := store.SaveTranslation(ctx, "public-excerpt-hash", "Лимиты восстановлены."); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if text, ok, err := store.Translation(ctx, "public-excerpt-hash"); err != nil || !ok || text != "Лимиты восстановлены." {
		t.Fatalf("translation=%q ok=%v error=%v", text, ok, err)
	}
}
