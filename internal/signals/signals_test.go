package signals

import (
	"reflect"
	"testing"
)

func TestRefs(t *testing.T) {
	text := "see https://github.com/Duro-Labs/Orchestration-Courier/pull/2 and acme/sessions-board#8, not foo#3 or a/b/c"
	got := Refs(text)
	want := []string{"duro-labs/orchestration-courier#2", "acme/sessions-board#8"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Refs = %v, want %v", got, want)
	}
	if Ref("Duro-Labs/X", 4) != "duro-labs/x#4" {
		t.Fatal("Ref not normalized")
	}
}

func TestPromptCleaning(t *testing.T) {
	if got := CleanPrompt("\n<pasted_content id=\"40f3\">\nread your state file\n</pasted_content>"); got != "read your state file" {
		t.Fatalf("CleanPrompt = %q", got)
	}
	for _, noise := range []string{"", "  ", "<command-name>/clear</command-name>", "Base directory for this skill: /x", "[Request interrupted by user]"} {
		if !IsNoisePrompt(noise) {
			t.Errorf("%q not noise", noise)
		}
	}
	if IsNoisePrompt("Add open router to the sources") {
		t.Error("real prompt flagged as noise")
	}
}

func TestClipAndLaunch(t *testing.T) {
	if got := Clip("a  b\n\nc", 10); got != "a b c" {
		t.Fatalf("Clip = %q", got)
	}
	if got := Clip("ééééé", 3); got != "ééé…" {
		t.Fatalf("Clip runes = %q", got)
	}
	if !IsLaunchCommand(`offload "sort this"`) || !IsLaunchCommand("hermes chat -Q -q x") || IsLaunchCommand("ls offloaded") {
		t.Fatal("launch detection wrong")
	}
}
