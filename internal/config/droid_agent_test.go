package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestDroidDefinitionAndDefaults(t *testing.T) {
	setupHome(t)
	if !contains(ExecutionAgents, "droid") || !HasReviewTemplate(nil, "droid") {
		t.Fatal("Droid must be selectable for execution and review")
	}
	d := AgentFor(nil, "droid")
	if d.Path != "droid" || d.ProcessName != "droid" || d.PromptDelivery.Mode != "argv" || *d.ExitCommand != "/quit" {
		t.Fatalf("definition=%+v", d)
	}
	if d.Session.Mode != "hook:droid-sessions" || !SessionDiscoversAfterStart(d.Session) || !SessionPersistsAfterStart(d.Session) || SessionResolvesEmptyReference(d.Session) {
		t.Fatalf("session=%+v", d.Session)
	}
	spec, ok := RulesSpec("droid")
	if !ok || spec.Global != ".factory/AGENTS.md" || spec.Project != "AGENTS.md" || spec.Integration != "markdown-reference" {
		t.Fatalf("rules=%+v", spec)
	}
	cfg := DefaultConfig()
	for _, section := range []map[string]string{cfg.Models.Kanban["droid"], cfg.Models.Review["droid"], cfg.Models.Chat["droid"]} {
		if len(section) == 0 {
			t.Fatal("missing model section")
		}
		for key, value := range section {
			if strings.HasSuffix(key, "model") && value != "" {
				t.Fatalf("Droid must inherit its own default for %s, got %q", key, value)
			}
		}
	}
	if !AgentSupportsEffort(cfg, "droid") || !ReviewModelSupportsEffort(cfg, "droid") {
		t.Fatal("Droid effort controls missing")
	}
	if d.Review.CWD != "root" || d.Review.Stdin != "none" || d.Review.HomePolicy != "optional" {
		t.Fatalf("review=%+v", d.Review)
	}
	want := []string{"exec", "--output-format", "json", "--only-tools", "Read,Grep,Glob,LS", "--model", "{model}", "--reasoning-effort", "{effort}", "{instruction}"}
	if !reflect.DeepEqual(d.Args.Review, want) {
		t.Fatalf("review argv=%q", d.Args.Review)
	}
}

func TestDroidSettingsPlaceholderIsNonRecursive(t *testing.T) {
	d := AgentFor(nil, "droid")
	got := ExpandAgentArgs(d.Args.Resume, "model", "high", "exact-id", "/cache/{model}/settings.json")
	want := []string{"--resume", "exact-id", "--auto", "high", "--settings", "/cache/{model}/settings.json"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%q want=%q", got, want)
	}
	if invalidPlaceholder("{droid_settings}") || !invalidPlaceholder("{droid_setting}") {
		t.Fatal("placeholder validation")
	}
	cfg := DefaultConfig()
	cfg.Agents = map[string]AgentDefinition{"wrapper": {Dialect: "droid"}}
	wrapper := AgentFor(cfg, "wrapper")
	if !reflect.DeepEqual(wrapper.Args.Start, d.Args.Start) || wrapper.Session.Mode != d.Session.Mode {
		t.Fatalf("wrapper=%+v", wrapper)
	}
	if HasReviewTemplate(cfg, "wrapper") {
		t.Fatal("an execution dialect wrapper must not inherit review authority")
	}
	if strings.Contains(strings.Join(d.Args.Start, " "), "{session}") {
		t.Fatal("Droid creates its own session ID")
	}
}
