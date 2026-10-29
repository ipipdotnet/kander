package launch

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dualface/kander/internal/board"
	"github.com/dualface/kander/internal/config"
	"github.com/dualface/kander/internal/process"
)

func TestDroidStartPersistsExactSessionAndNotifyResumesIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shared tmux fake is POSIX")
	}
	root, _, _ := setupBoard(t)
	isolateDroid(t)
	cfg := envConfig("droid", "tmux", nil)
	loadEffective = func() (*config.Config, error) { return cfg, nil }
	id, _ := makeTodo(t, root, "droid-start")
	var captured []string
	previous := newShellInvocation
	newShellInvocation = func(p process.AgentProgram, args []string, env map[string]string) (process.ProcessInvocation, error) {
		captured = append([]string{}, args...)
		writeDroidSession(t, "project", "native-droid-id", parentDir(root), id)
		return previous(p, args, env)
	}
	t.Cleanup(func() { newShellInvocation = previous })
	if _, err := Start(root, "droid", "tmux", id); err != nil {
		t.Fatal(err)
	}
	snapshot, err := board.ReadSnapshot(root, id)
	if err != nil || snapshot.Entry.State != "working" || !strings.Contains(snapshot.Text, "- SESSION: droid native-droid-id\n") {
		t.Fatalf("session not persisted: %+v %v", snapshot, err)
	}
	if !strings.Contains(captured[len(captured)-1], "UTF-8 task file") {
		t.Fatalf("prompt missing: %q", captured)
	}
	t.Setenv("KANBAN_TMUX_CURRENT_COMMAND", "droid")
	t.Setenv("KANBAN_TMUX_PANE_SESSION", "native-droid-id")
	if _, err := NotifyViaResume(root, snapshot.Entry, snapshot.Text, "continue verification", 61); err != nil {
		t.Fatal(err)
	}
	if len(captured) < 2 || captured[0] != "--resume" || captured[1] != "native-droid-id" {
		t.Fatalf("notify recovery lost exact session: %q", captured)
	}
}

func TestDroidAmbiguousStartRollsBackAndRemovesTaskFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shared tmux fake is POSIX")
	}
	root, _, _ := setupBoard(t)
	isolateDroid(t)
	id, path := makeTodo(t, root, "droid-ambiguous")
	original := mustRead(t, path)
	_, taskFile := captureTaskFile(t)
	previous := newShellInvocation
	newShellInvocation = func(p process.AgentProgram, args []string, env map[string]string) (process.ProcessInvocation, error) {
		writeDroidSession(t, "project", "first", parentDir(root), id)
		writeDroidSession(t, "project", "second", parentDir(root), id)
		return previous(p, args, env)
	}
	t.Cleanup(func() { newShellInvocation = previous })
	if _, err := Start(root, "droid", "tmux", id); err == nil {
		t.Fatal("ambiguous start succeeded")
	}
	if got := mustRead(t, path); got != original {
		t.Fatalf("rollback changed todo card: %s", got)
	}
	if *taskFile == "" {
		t.Fatal("no task file was created")
	}
	if _, err := os.Stat(*taskFile); !os.IsNotExist(err) {
		t.Fatalf("failed launch task file remains: %v", err)
	}
	if got := mustRead(t, filepath.Join(root, "tmux.log.kill")); !strings.Contains(got, "kill-window") {
		t.Fatalf("failed container not closed: %s", got)
	}
}
