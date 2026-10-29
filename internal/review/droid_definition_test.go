package review

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dualface/kander/internal/config"
	"github.com/dualface/kander/internal/process"
)

func TestDroidReviewInvocationIsReadOnlyAndUsesArgvInstruction(t *testing.T) {
	t.Setenv(config.EnvConfig, filepath.Join(t.TempDir(), "missing.json"))
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	settings, err := agentSettingsFor("droid", "PMQA", "large")
	if err != nil {
		t.Fatal(err)
	}
	if settings.stdin != config.ReviewStdinNone || settings.homePolicy != config.ReviewHomeOptional || !settings.snapshotSpec {
		t.Fatalf("settings=%+v", settings)
	}
	settings.model, settings.effort = "custom:model", "high"
	ctx := reviewContext{
		agent: "droid", settings: settings, root: "/work",
		program:     process.AgentProgram{Path: "/bin/droid"},
		instruction: mustInstruction(t, "Perform the PMQA review.", "/rt/prompt.txt"),
	}
	inv, cwd, err := reviewerArguments(ctx, "/rt", "/rt/out", "/rt/prompt.txt")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"exec", "--output-format", "json", "--only-tools", "Read,Grep,Glob,LS", "--model", "custom:model", "--reasoning-effort", "high", ctx.instruction}
	if cwd != "/work" || !reflect.DeepEqual(inv.Argv[1:], want) {
		t.Fatalf("cwd=%s argv=%q", cwd, inv.Argv)
	}
	if !usesLastMessageReport("droid") {
		t.Fatal("Droid must use the final-message report contract")
	}
	ctx.settings.model, ctx.settings.effort = "", ""
	inv, _, err = reviewerArguments(ctx, "/rt", "/rt/out", "/rt/prompt.txt")
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"exec", "--output-format", "json", "--only-tools", "Read,Grep,Glob,LS", ctx.instruction}
	if !reflect.DeepEqual(inv.Argv[1:], want) {
		t.Fatalf("empty options were not omitted: %q", inv.Argv)
	}
}
