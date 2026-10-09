package issues

import (
	"strings"
	"testing"
)

func want(key string) Want {
	return Want{Key: key, Title: "title " + key, Body: "body " + key, Labels: []string{"animap"}}
}

func kinds(actions []Action) map[string]Kind {
	out := map[string]Kind{}
	for _, a := range actions {
		out[a.Key] = a.Kind
	}
	return out
}

func TestPlan(t *testing.T) {
	existing := []Issue{
		{Number: 1, State: "OPEN", Title: "title a", Body: Render(new(want("a")))},
		{Number: 2, State: "OPEN", Title: "title b", Body: "stale\n\n" + Marker("b")},
		{Number: 3, State: "CLOSED", Title: "title c", Body: Render(new(want("c")))},
		{Number: 4, State: "OPEN", Title: "x", Body: Marker("gone")},
		{Number: 5, State: "OPEN", Title: "x", Body: Marker("unlooked")},
		{Number: 6, State: "OPEN", Title: "human issue", Body: "no marker"},
	}
	evaluated := map[string]bool{"a": true, "b": true, "c": true, "d": true, "gone": true}
	actions, deferred := Plan([]Want{want("a"), want("b"), want("c"), want("d")}, evaluated, existing, 10)
	got := kinds(actions)
	wantKinds := map[string]Kind{"b": edit, "c": reopen, "d": Create, "gone": Close}
	if len(got) != len(wantKinds) || len(deferred) != 0 {
		t.Fatalf("Plan = %+v, deferred %v", actions, deferred)
	}
	for k, v := range wantKinds {
		if got[k] != v {
			t.Errorf("Plan action for %q = %q, want %q", k, got[k], v)
		}
	}
	for _, a := range actions {
		if a.Kind == reopen && a.Number != 3 || a.Kind == Close && a.Number != 4 {
			t.Errorf("action %+v targets the wrong issue", a)
		}
	}
}

func TestPlanCapsCreates(t *testing.T) {
	ws := []Want{want("a"), want("b"), want("c")}
	actions, deferred := Plan(ws, nil, nil, 2)
	if len(actions) != 2 || len(deferred) != 1 || deferred[0].Key != "c" {
		t.Errorf("Plan with limit 2 = %d actions, deferred %+v", len(actions), deferred)
	}
	open := []Issue{{Number: 1, State: "OPEN", Title: "title a", Body: "old " + Marker("a")}}
	actions, _ = Plan([]Want{want("a")}, nil, open, 0)
	if len(actions) != 1 || actions[0].Kind != edit {
		t.Errorf("an edit is not a create and must not be capped: %+v", actions)
	}
}

func TestPlanReopensNewestClosed(t *testing.T) {
	for _, closed := range [][]Issue{
		{{Number: 3, State: "CLOSED", Body: Marker("a")}, {Number: 9, State: "CLOSED", Body: Marker("a")}},
		{{Number: 9, State: "CLOSED", Body: Marker("a")}, {Number: 3, State: "CLOSED", Body: Marker("a")}},
	} {
		actions, _ := Plan([]Want{want("a")}, nil, closed, 5)
		if len(actions) != 1 || actions[0].Number != 9 {
			t.Errorf("Plan over %+v = %+v, want reopen #9", closed, actions)
		}
	}
}

func TestMarker(t *testing.T) {
	if KeyOf("text\n"+Marker("drift:tvdb:544")+"\n") != "drift:tvdb:544" {
		t.Error("KeyOf did not read the marker back")
	}
	if KeyOf("<!-- animap:key=Bad Key -->") != "" {
		t.Error("KeyOf accepted a malformed key")
	}
	long := Render(&Want{Key: "k", Body: strings.Repeat("é", MaxBodyRunes+10)})
	if strings.Count(long, "é") != MaxBodyRunes || !strings.Contains(long, "(truncated)") || KeyOf(long) != "k" {
		t.Error("Render did not bound the body and keep the marker")
	}
}

func TestUntrusted(t *testing.T) {
	got := Untrusted("@maintainer `code` [link](https://x)\nnext line" + strings.Repeat("x", 300))
	if !strings.HasPrefix(got, "`") || !strings.HasSuffix(got, "`") || strings.Count(got, "`") != 2 {
		t.Errorf("Untrusted = %q, want one code span", got)
	}
	if strings.Contains(got, "\n") || len(got) > 210 {
		t.Errorf("Untrusted = %q, want one bounded line", got)
	}
}

func TestPlanCarriesPin(t *testing.T) {
	a, _ := Plan([]Want{{Key: "watch:unmappable", Title: "t", Pin: true}}, nil, nil, 10)
	if len(a) != 1 || a[0].Kind != Create || !a[0].Pin {
		t.Errorf("Plan = %+v, want one pinned create", a)
	}
}
