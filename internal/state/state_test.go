package state

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStateSavesAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	store, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(data *Data) {
		data.LastChat = -100123
		data.Notify = map[int64][]string{111: {"completed"}, 222: nil}
		data.Sorts = map[int64]string{111: "rev size"}
	}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("state file mode = %v, want 0600", info.Mode().Perm())
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	reloaded.Read(func(data *Data) {
		if data.Version != version || data.LastChat != -100123 || data.Sorts[111] != "rev size" ||
			len(data.Notify) != 2 || data.Notify[111][0] != "completed" {
			t.Fatalf("reloaded state = %+v", data)
		}
	})
}

// A damaged file, or one from a newer version, is refused rather than
// silently replaced, which would lose what the bot remembered.
func TestStateRefusesDamagedOrNewerFiles(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"damaged.json": `{"version": 1, "sorts": `,
		"newer.json":   `{"version": 99}`,
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil || !strings.Contains(err.Error(), path) {
			t.Errorf("%s: Load = %v, want an error naming the file", name, err)
		}
	}
}

// Without a path, or before the first save, the state is empty and lives
// in memory.
func TestStateWithoutAFile(t *testing.T) {
	for _, path := range []string{"", filepath.Join(t.TempDir(), "missing.json")} {
		store, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		store.Read(func(data *Data) {
			if data.LastChat != 0 || data.Notify != nil {
				t.Fatalf("fresh state = %+v", data)
			}
		})
	}
	memory, _ := Load("")
	if err := memory.Update(func(data *Data) { data.LastChat = 1 }); err != nil {
		t.Fatalf("an in-memory update failed: %v", err)
	}
	memory.Read(func(data *Data) {
		if data.LastChat != 1 {
			t.Fatal("an in-memory update was lost")
		}
	})
}

// A state that cannot be saved keeps its change in memory and says so.
func TestStateKeepsChangesItCannotSave(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// The state's directory would have to be inside a file.
	store, err := Load(filepath.Join(blocker, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(data *Data) { data.LastChat = 7 }); err == nil {
		t.Fatal("expected the save to fail")
	}
	store.Read(func(data *Data) {
		if data.LastChat != 7 {
			t.Fatal("the change was lost with the save")
		}
	})
}
