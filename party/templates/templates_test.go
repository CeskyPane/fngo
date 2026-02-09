package templates

import "testing"

func TestDefaultTemplatesReturnIndependentCopies(t *testing.T) {
	first := DefaultPartyMeta()
	second := DefaultPartyMeta()

	if &first == &second {
		t.Fatalf("expected independent map instances")
	}

	first["Default:CustomMatchKey_s"] = "alpha"
	if second["Default:CustomMatchKey_s"] == "alpha" {
		t.Fatalf("expected mutation isolation")
	}

	memberA := DefaultPartyMemberMeta()
	memberB := DefaultPartyMemberMeta()

	if !PathSet(memberA, "Default:LobbyState_j", map[string]any{"LobbyState": map[string]any{"gameReadiness": "Ready"}}) {
		t.Fatalf("expected path set to succeed")
	}

	if got, _ := PathGet(memberB, "Default:LobbyState_j"); got == nil {
		t.Fatalf("expected untouched template value")
	}
}

func TestDeepMergeDeterministic(t *testing.T) {
	dst := map[string]any{
		"a": "one",
		"j": map[string]any{
			"x": 1,
			"y": 2,
		},
	}
	src := map[string]any{
		"j": map[string]any{
			"y": 20,
			"z": 30,
		},
	}

	merged := DeepMerge(dst, src)

	if merged["a"] != "one" {
		t.Fatalf("expected original scalar to remain")
	}

	j, ok := merged["j"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested map")
	}

	if j["x"].(int) != 1 {
		t.Fatalf("expected nested x to remain")
	}

	if j["y"].(int) != 20 {
		t.Fatalf("expected nested y override")
	}

	if j["z"].(int) != 30 {
		t.Fatalf("expected nested z insert")
	}
}

func TestPathSetAndGet(t *testing.T) {
	root := map[string]any{}
	if !PathSet(root, "Default:LobbyState_j.LobbyState.gameReadiness", "Ready") {
		t.Fatalf("expected path set")
	}

	value, ok := PathGet(root, "Default:LobbyState_j.LobbyState.gameReadiness")
	if !ok {
		t.Fatalf("expected value at path")
	}

	if value != "Ready" {
		t.Fatalf("unexpected path value: %v", value)
	}
}
