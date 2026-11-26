package state

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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
		data.Notify = map[Place][]string{{Chat: 111}: {"completed"}, {Chat: 222}: nil}
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
		if data.Version != 1 || data.LastChat != -100123 || data.Sorts[111] != "rev size" ||
			len(data.Notify) != 2 || data.Notify[Place{Chat: 111}][0] != "completed" {
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

// A group that becomes a supergroup keeps what the bot remembered of it,
// under its new ID, except its status message; what the new chat already
// has wins.
func TestMoveChat(t *testing.T) {
	old, moved := Place{Chat: -4001}, Place{Chat: -1009}
	data := Data{
		LastChat: -4001,
		Notify:   map[Place][]string{old: {"completed", "errors"}, {Chat: 7}: {"disk"}},
		Sorts:    map[int64]string{-4001: "rev size", -1009: "name"},
		Digest:   map[Place]Digest{old: {Time: "08:00"}},
		Status:   map[Place]int{old: 12},
	}
	if !data.MoveChat(-4001, -1009) {
		t.Fatal("MoveChat reported no change")
	}
	if data.LastChat != -1009 || len(data.Notify[moved]) != 2 || data.Notify[Place{Chat: 7}][0] != "disk" || data.Sorts[-1009] != "name" ||
		data.Digest[moved].Time != "08:00" || len(data.Status) != 0 {
		t.Fatalf("after the move: %+v", data)
	}
	for _, gone := range []bool{data.Notify[old] != nil, data.Sorts[-4001] != "", data.Digest[old].Time != ""} {
		if gone {
			t.Fatalf("the old chat is still remembered: %+v", data)
		}
	}
	if data.MoveChat(-4001, -1009) {
		t.Fatal("a second move reported a change")
	}
	var empty Data
	if empty.MoveChat(1, 2) {
		t.Fatal("moving a chat of empty state reported a change")
	}
}

// A chat's topics move with it; what the new chat already has in a topic
// wins.
func TestMoveChatMovesItsTopics(t *testing.T) {
	data := Data{
		LastChat: -4001, LastTopic: 5,
		Notify: map[Place][]string{{Chat: -4001, Topic: 5}: {"errors"}, {Chat: -4001, Topic: 6}: {"disk"}, {Chat: -1009, Topic: 6}: {"completed"}},
		Digest: map[Place]Digest{{Chat: -4001, Topic: 5}: {Time: "08:00"}},
		Status: map[Place]int{{Chat: -4001, Topic: 5}: 12, {Chat: 7, Topic: 5}: 13},
	}
	if !data.MoveChat(-4001, -1009) {
		t.Fatal("MoveChat reported no change")
	}
	want := map[Place][]string{{Chat: -1009, Topic: 5}: {"errors"}, {Chat: -1009, Topic: 6}: {"completed"}}
	if !maps.EqualFunc(data.Notify, want, slices.Equal) {
		t.Errorf("notifications after the move = %v, want %v", data.Notify, want)
	}
	if data.Last() != (Place{Chat: -1009, Topic: 5}) || data.Digest[Place{Chat: -1009, Topic: 5}].Time != "08:00" || len(data.Digest) != 1 {
		t.Errorf("after the move: %+v", data)
	}
	if len(data.Status) != 1 || data.Status[Place{Chat: 7, Topic: 5}] != 13 {
		t.Errorf("status messages after the move = %v, want only another chat's", data.Status)
	}
}

// Places are written as the chat's ID, as before topics, or as chat:topic,
// and read back the same; anything else is refused.
func TestPlaceText(t *testing.T) {
	for _, tc := range []struct {
		place Place
		text  string
	}{
		{Place{Chat: -1001234567890}, "-1001234567890"},
		{Place{Chat: -1001234567890, Topic: 45}, "-1001234567890:45"},
		{Place{Chat: 42}, "42"},
	} {
		text, err := tc.place.MarshalText()
		if err != nil || string(text) != tc.text {
			t.Errorf("%+v is written %q, %v; want %q", tc.place, text, err, tc.text)
		}
		var read Place
		if err := read.UnmarshalText([]byte(tc.text)); err != nil || read != tc.place {
			t.Errorf("%q is read as %+v, %v; want %+v", tc.text, read, err, tc.place)
		}
	}
	for _, bad := range []string{"", "x", "-100:", "-100:0", "-100:-3", "-100:x", ":5", "1.5"} {
		var read Place
		if err := read.UnmarshalText([]byte(bad)); err == nil {
			t.Errorf("%q is read as %+v, want an error", bad, read)
		}
	}
}

// A state with no topic's notifications, status, or digest is saved as
// format 1, which older versions read, even when the last place a master
// used is a topic; one with them as format 2, which older versions refuse
// rather than misread. This version reads both.
func TestStateFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	older := `{"version": 1, "lastChat": -100123, "notify": {"-100123": ["completed"], "7": []},` +
		` "status": {"-100123": 5}, "digest": {"7": {"time": "08:00", "lastDue": "2026-11-01", "since": 0, "uploaded": 0, "downloaded": 0}}}`
	if err := os.WriteFile(path, []byte(older), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	store.Read(func(data *Data) {
		if data.Last() != (Place{Chat: -100123}) || data.Notify[Place{Chat: -100123}][0] != "completed" ||
			data.Status[Place{Chat: -100123}] != 5 || data.Digest[Place{Chat: 7}].Time != "08:00" {
			t.Fatalf("a format 1 file is read as %+v", data)
		}
	})
	saved := func() map[string]any {
		t.Helper()
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var file map[string]any
		if err := json.Unmarshal(content, &file); err != nil {
			t.Fatal(err)
		}
		return file
	}

	if err := store.Update(func(data *Data) { data.Sorts = map[int64]string{7: "size"} }); err != nil {
		t.Fatal(err)
	}
	if file := saved(); file["version"] != 1.0 || file["notify"].(map[string]any)["-100123"] == nil {
		t.Errorf("without topics, the state is saved as %v", file)
	}

	for name, change := range map[string]func(*Data){
		"notifications": func(data *Data) { data.Notify[Place{Chat: -100123, Topic: 45}] = []string{"errors"} },
		"status":        func(data *Data) { data.Status[Place{Chat: -100123, Topic: 45}] = 9 },
		"digest":        func(data *Data) { data.Digest[Place{Chat: -100123, Topic: 45}] = Digest{Time: "09:00"} },
	} {
		if err := store.Update(change); err != nil {
			t.Fatal(err)
		}
		file := saved()
		if file["version"] != 2.0 {
			t.Errorf("with a topic's %s, the state is saved as format %v", name, file["version"])
		}
		reloaded, err := Load(path)
		if err != nil {
			t.Fatalf("with a topic's %s: %v", name, err)
		}
		reloaded.Read(func(data *Data) {
			if data.Version != 2 {
				t.Errorf("with a topic's %s, the state is reloaded as format %d", name, data.Version)
			}
		})
		if err := store.Update(func(data *Data) {
			delete(data.Notify, Place{Chat: -100123, Topic: 45})
			delete(data.Status, Place{Chat: -100123, Topic: 45})
			delete(data.Digest, Place{Chat: -100123, Topic: 45})
		}); err != nil {
			t.Fatal(err)
		}
		if file := saved(); file["version"] != 1.0 {
			t.Errorf("without the topic's %s again, the state is saved as format %v", name, file["version"])
		}
	}

	if err := store.Update(func(data *Data) { data.LastTopic = 45 }); err != nil {
		t.Fatal(err)
	}
	if file := saved(); file["version"] != 1.0 || file["lastTopic"] != 45.0 {
		t.Errorf("with only the last place in a topic, the state is saved as %v", file)
	}

	if err := os.WriteFile(path, []byte(`{"version": 1, "notify": {"-100123:x": []}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("a file with a malformed place was read")
	}
}

// A topic that is gone is forgotten: its notifications go nowhere else, and
// its digest and status end; the chat's other places keep theirs.
func TestForgetTopic(t *testing.T) {
	gone, other, general := Place{Chat: -100, Topic: 5}, Place{Chat: -100, Topic: 6}, Place{Chat: -100}
	data := Data{
		LastChat: -100, LastTopic: 5,
		Notify: map[Place][]string{gone: {"completed"}, other: {"errors"}, general: {"disk"}},
		Digest: map[Place]Digest{gone: {Time: "08:00"}, general: {Time: "09:00"}},
		Status: map[Place]int{gone: 3, other: 4},
	}
	if !data.ForgetTopic(gone) {
		t.Fatal("ForgetTopic reported no change")
	}
	if kinds, ok := data.Notify[gone]; ok {
		t.Errorf("the topic that is gone still chose %v", kinds)
	}
	if len(data.Notify[other]) != 1 || len(data.Notify[general]) != 1 || len(data.Digest) != 1 || data.Digest[general].Time != "09:00" ||
		len(data.Status) != 1 || data.Status[other] != 4 || data.Last() != (Place{}) {
		t.Errorf("after the topic is gone: %+v", data)
	}
	if data.ForgetTopic(gone) {
		t.Error("forgetting the topic again reported a change")
	}
	if data.ForgetTopic(general) {
		t.Error("a chat itself was forgotten as a topic")
	}

	// When the only place to choose was the topic, its chat is kept as
	// having chosen none, so that the default does not send the topic's
	// notifications elsewhere; and the state no longer needs format 2.
	alone := Data{LastChat: -100, Notify: map[Place][]string{gone: {"completed"}}}
	alone.ForgetTopic(gone)
	if want := map[Place][]string{general: {}}; !maps.EqualFunc(alone.Notify, want, slices.Equal) {
		t.Errorf("after the only topic is gone, the choices are %v, want %v", alone.Notify, want)
	}
	if alone.format() != 1 {
		t.Errorf("after the only topic is gone, the state needs format %d", alone.format())
	}
}
