package launch

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dualface/kander/internal/board"
	"github.com/dualface/kander/internal/config"
)

func isolateDroid(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", home)
	t.Setenv("XDG_CACHE_HOME", home)
	return home
}

func writeDroidSession(t *testing.T, folder, id, cwd string, tasks ...string) string {
	t.Helper()
	root, err := droidSessionsRoot()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, folder)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"type": "session_start", "id": id, "cwd": cwd, "version": 2})
	task := "task"
	if len(tasks) > 0 {
		task = tasks[0]
	}
	message, _ := json.Marshal(map[string]any{"type": "message", "message": map[string]any{"role": "user", "content": []map[string]string{{"type": "text", "text": "Execute Kanban task " + task + ". Read the task file."}}}})
	data = append(append(append(data, '\n'), message...), '\n')
	path := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDroidSettingsAcrossExecutionChatAndResume(t *testing.T) {
	home := isolateDroid(t)
	cfg := config.DefaultConfig()
	cfg.Models.Kanban["droid"]["large_model"] = "custom:model-with-\"quotes\""
	cfg.Models.Kanban["droid"]["large_effort"] = "high"
	session := AgentSession{Agent: "droid", Reference: "exact-session"}
	for _, resume := range []bool{false, true} {
		args, err := agentArguments("droid", cfg.Models.Kanban["droid"], "large", session, resume, cfg)
		if err != nil {
			t.Fatal(err)
		}
		path := args[len(args)-1]
		if !strings.HasPrefix(path, home) {
			t.Fatalf("settings escaped isolated cache: %q", path)
		}
		var settings map[string]string
		if err := json.Unmarshal([]byte(mustRead(t, path)), &settings); err != nil {
			t.Fatal(err)
		}
		if settings["model"] != cfg.Models.Kanban["droid"]["large_model"] || settings["reasoningEffort"] != "high" {
			t.Fatalf("settings=%v", settings)
		}
		want := []string{"--auto", "high", "--settings", path}
		if resume {
			want = append([]string{"--resume", "exact-session"}, want...)
		}
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("args=%q want=%q", args, want)
		}
	}
	args, err := chatAgentArguments("droid", "chat-model", "low", session, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, args[len(args)-1]); got != `{"model":"chat-model","reasoningEffort":"low"}` {
		t.Fatalf("chat settings=%s", got)
	}
	if !board.ExecutionTakesEffort(cfg, "droid") || !board.ReviewTakesEffort(cfg, "droid") {
		t.Fatal("Droid card effort pins must be supported")
	}
	// Native configuration is never created or rewritten.
	if _, err := os.Stat(filepath.Join(home, ".factory", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("native settings touched: %v", err)
	}
}

func TestDroidSettingsCacheDefaultsReuseAndTamper(t *testing.T) {
	isolateDroid(t)
	template := config.AgentFor(nil, "droid").Args.Start
	path, err := droidSettingsFile(template, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, path); got != "{}" {
		t.Fatalf("empty settings=%s", got)
	}
	again, err := droidSettingsFile(template, "", "")
	if err != nil || again != path {
		t.Fatalf("cache reuse=%q %v", again, err)
	}
	if err := os.WriteFile(path, []byte(`{"model":"changed"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := droidSettingsFile(template, "", ""); err == nil {
		t.Fatal("modified settings cache accepted")
	}
	if path, err := droidSettingsFile([]string{"{model}"}, "x", "high"); err != nil || path != "" {
		t.Fatalf("non-Droid launch created a file: %q %v", path, err)
	}
}

func TestDroidSessionSnapshotDiscoveryAndAmbiguity(t *testing.T) {
	isolateDroid(t)
	cwd := t.TempDir()
	writeDroidSession(t, "project", "existing", cwd)
	partial := writeDroidSession(t, "project", "unfinished", cwd)
	if err := os.WriteFile(partial, nil, 0600); err != nil {
		t.Fatal(err)
	}
	def := config.AgentFor(nil, "droid").Session
	before, err := sessionDiscoverSnapshot(def, "task", true, nil, cwd, LaunchPlan{}.launchTimeout)
	if err != nil || len(before) != 2 {
		t.Fatalf("snapshot=%v %v", before, err)
	}
	writeDroidSession(t, "project", "unfinished", cwd)
	writeDroidSession(t, "other", "other-project", t.TempDir())
	unrelated := writeDroidSession(t, "project", "unrelated", cwd)
	if err := os.WriteFile(unrelated, []byte(strings.ReplaceAll(mustRead(t, unrelated), "task task.", "task other.")), 0600); err != nil {
		t.Fatal(err)
	}
	writeDroidSession(t, "project", "new-session", cwd)
	id, err := runSessionDiscoverHook(def, "task", before, time.Now(), nil, cwd, LaunchPlan{}.launchTimeout)
	if err != nil || id != "new-session" {
		t.Fatalf("discovery=%q %v", id, err)
	}
	writeDroidSession(t, "project", "another-session", cwd)
	if _, err := discoverDroidSession(before, "task", cwd, time.Second); err == nil {
		t.Fatal("ambiguous sessions must never select the latest")
	}
}

func TestDroidSessionMissingPartialInvalidAndCanceled(t *testing.T) {
	isolateDroid(t)
	cwd := t.TempDir()
	before, err := snapshotDroidSessions(time.Second)
	if err != nil || len(before) != 0 {
		t.Fatalf("fresh home=%v %v", before, err)
	}
	if _, err := discoverDroidSession(before, "task", cwd, time.Millisecond); err == nil {
		t.Fatal("missing session must time out")
	}
	path := writeDroidSession(t, "project", "session", cwd)
	for _, body := range []string{"{", strings.Repeat("x", 17000), `{"type":"other"}` + "\n"} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if id, err := droidSessionHeader(path, cwd); err != nil || id != "" {
			t.Fatalf("invalid header bound: %q %v", id, err)
		}
	}
	writeDroidSession(t, "project", "session", cwd)
	data := strings.Replace(mustRead(t, path), `"id":"session"`, `"id":"different"`, 1)
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := droidSessionHeader(path, cwd); err == nil {
		t.Fatal("header/filename mismatch accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := droidSessionPaths(ctx); err == nil {
		t.Fatal("canceled scan accepted")
	}
}

func TestDroidSessionRejectsSymlink(t *testing.T) {
	isolateDroid(t)
	cwd := t.TempDir()
	path := writeDroidSession(t, "project", "session", cwd)
	link := filepath.Join(filepath.Dir(path), "linked.jsonl")
	if err := os.Symlink(path, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := droidSessionHeader(link, cwd); err == nil {
		t.Fatal("session symlink accepted")
	}
}
